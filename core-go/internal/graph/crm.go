package graph

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Confidence of identities and edges inferred by deterministic rules rather than stated by a
// system of record.
const (
	ruleConfidence    = 0.9
	speakerConfidence = 0.8
	exactConfidence   = 1.0
	// recorderConfidence is the confidence of a call label paired with an email by the recorder.
	recorderConfidence = 0.95
)

type crmFieldChange struct {
	New json.RawMessage `json:"new"`
}

// crmEvent is the part of a crm_change payload WP5 reads.
type crmEvent struct {
	Kind            string                    `json:"kind"`
	ObjectType      string                    `json:"object_type"`
	RecordID        string                    `json:"record_id"`
	AccountRecordID *string                   `json:"account_record_id"`
	Created         bool                      `json:"created"`
	Fields          map[string]crmFieldChange `json:"fields"`
}

// decodeCRM returns the payload of a CRM source event; ok is false for any other event. A CRM
// event whose payload cannot be read is an error: normalization accepted it, so this means
// the stored data and this code disagree.
func decodeCRM(ev normalize.SourceEvent) (crmEvent, bool, error) {
	if ev.SourceSystem != "crm" {
		return crmEvent{}, false, nil
	}
	var c crmEvent
	if err := json.Unmarshal(ev.Payload, &c); err != nil {
		return crmEvent{}, false, fmt.Errorf("graph: decode crm payload of %s: %w", ev.SourceObjectID, err)
	}
	if c.Kind != "crm_change" {
		return crmEvent{}, false, fmt.Errorf("graph: crm event %s has payload kind %q", ev.SourceObjectID, c.Kind)
	}
	return c, true, nil
}

func (c crmEvent) has(name string) bool {
	f, ok := c.Fields[name]
	return ok && f.New != nil
}

// field is the new value of a field rendered as text; "" when absent or null.
func (c crmEvent) field(name string) string {
	f, ok := c.Fields[name]
	if !ok || len(f.New) == 0 || string(f.New) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(f.New, &s) == nil {
		return strings.TrimSpace(s)
	}
	var buf bytes.Buffer
	if json.Compact(&buf, f.New) != nil {
		return string(f.New)
	}
	return buf.String()
}

func (c crmEvent) accountRecord() string {
	if c.AccountRecordID == nil {
		return ""
	}
	return *c.AccountRecordID
}

// prepared is what Prepare hands to Finalize.
type prepared struct {
	mappingIDs      []string // created here; Finalize stamps the evidence activity
	entitiesChanged bool
}

var _ ingest.Prepared = (*prepared)(nil)

// EntitiesChanged implements ingest.Prepared.
func (st *prepared) EntitiesChanged() bool { return st != nil && st.entitiesChanged }

// ensureMapping opens a mapping and remembers it when this call created it.
func (st *prepared) ensureMapping(ctx context.Context, q Txn, m Mapping) (Mapping, error) {
	got, created, err := EnsureMapping(ctx, q, m)
	if err == nil && created {
		st.mappingIDs = append(st.mappingIDs, got.ID)
		st.entitiesChanged = true
	}
	return got, err
}

// Prepare implements ingest.Extension: CRM "created" events create the account, opportunity
// or contact they describe, with exact mappings, so the resolver can find them.
func (e *Extension) Prepare(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (ingest.Prepared, error) {
	c, ok, err := decodeCRM(ev)
	if err != nil || !ok || !c.Created {
		return nil, err
	}
	st := &prepared{}
	at := act.OccurredAt()
	switch c.ObjectType {
	case "Account":
		err = prepareAccount(ctx, tx, st, c, at)
	case "Opportunity":
		err = prepareOpportunity(ctx, tx, st, c, at)
	case "Contact":
		err = prepareContact(ctx, tx, st, c, at)
	}
	if err != nil {
		return nil, err
	}
	return st, nil
}

func prepareAccount(ctx context.Context, tx Txn, st *prepared, c crmEvent, at time.Time) error {
	if err := lock(ctx, tx, "crm-record", c.RecordID); err != nil {
		return err
	}
	if _, ok, err := CurrentMapping(ctx, tx, CRMKey(c.RecordID)); err != nil || ok {
		return err
	}
	domain := accountDomain(c)
	name := c.field("Name")
	if name == "" {
		name = c.RecordID
	}
	id := ""
	if domain != "" {
		if err := lock(ctx, tx, "account-domain", domain); err != nil {
			return err
		}
		var err error
		if id, err = accountByDomain(ctx, tx, domain); err != nil {
			return err
		}
	}
	if id == "" {
		if err := tx.QueryRowContext(ctx, `INSERT INTO accounts (name, domain) VALUES ($1, $2) RETURNING id::text`, name, optional(domain)).Scan(&id); err != nil {
			return fmt.Errorf("graph: create account %s: %w", c.RecordID, err)
		}
		st.entitiesChanged = true
	}
	maps := []Mapping{{SourceKey: CRMKey(c.RecordID), Method: MethodExact, Confidence: exactConfidence}}
	if domain != "" {
		maps = append(maps, Mapping{SourceKey: SourceKey{"domain", domain}, Method: MethodExact, Confidence: exactConfidence})
	}
	if slug := slackSlug(domain, name); slug != "" {
		maps = append(maps, Mapping{SourceKey: SourceKey{"slack", "#deal-" + slug}, Method: MethodRule, Confidence: speakerConfidence,
			Evidence: evidenceJSON(map[string]string{"rule": "deal_channel_slug", "slug": slug})})
	}
	for _, m := range maps {
		m.EntityType, m.EntityID, m.ValidFrom = EntityAccount, id, at
		if _, err := st.ensureMapping(ctx, tx, m); err != nil {
			return err
		}
	}
	return nil
}

func accountDomain(c crmEvent) string {
	for _, f := range []string{"Website", "Domain", "Domain__c"} {
		if d := normalize.DomainFromWebsite(c.field(f)); d != "" {
			return d
		}
	}
	return ""
}

// slackSlug names the account's #deal-<slug> channel: the first label of its domain, else the
// first word of its name, reduced to lower-case letters and digits.
func slackSlug(domain, name string) string {
	src := name
	if domain != "" {
		src, _, _ = strings.Cut(domain, ".")
	}
	src = strings.ToLower(strings.Fields(src + " ")[0])
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, src)
}

// accountByDomain finds an account by accounts.domain or a domain mapping. A personal mail
// domain (gmail.com, ...) identifies no account.
func accountByDomain(ctx context.Context, q DBTX, domain string) (string, error) {
	if normalize.IsPersonalDomain(domain) {
		return "", nil
	}
	var id string
	err := q.QueryRowContext(ctx, `
SELECT id FROM (
    SELECT a.id::text AS id, 0 AS rank FROM accounts a WHERE a.domain = $1
    UNION ALL
    SELECT m.entity_id::text, 1 FROM entity_source_mappings m
     WHERE m.entity_type = 'account' AND m.valid_to IS NULL AND m.source_system = 'domain' AND m.source_key = $1
) c ORDER BY rank, id LIMIT 1`, domain).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("graph: account by domain: %w", err)
	}
	return id, nil
}

// entityByCRM returns the entity a CRM record maps to.
func entityByCRM(ctx context.Context, q DBTX, record string) (string, error) {
	if record == "" {
		return "", nil
	}
	m, ok, err := CurrentMapping(ctx, q, CRMKey(record))
	if err != nil || !ok {
		return "", err
	}
	return m.EntityID, nil
}

// opportunityMotion maps a CRM opportunity Type to opportunities.motion.
func opportunityMotion(crmType string) string {
	switch strings.ToLower(crmType) {
	case "expansion":
		return "expansion"
	case "renewal":
		return "renewal"
	}
	return "new_business"
}

func prepareOpportunity(ctx context.Context, tx Txn, st *prepared, c crmEvent, at time.Time) error {
	if err := lock(ctx, tx, "crm-record", c.RecordID); err != nil {
		return err
	}
	acct, err := entityByCRM(ctx, tx, c.accountRecord())
	if err != nil || acct == "" {
		return err // the account has not arrived yet: the event is parked and retried
	}
	if _, ok, err := CurrentMapping(ctx, tx, CRMKey(c.RecordID)); err != nil || ok {
		return err
	}
	name := c.field("Name")
	if name == "" {
		name = c.RecordID
	}
	owner, err := employeeByEmail(ctx, tx, c.field("OwnerEmail"))
	if err != nil {
		return err
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO opportunities (account_id, name, motion, owner_person_id)
		VALUES ($1::uuid, $2, $3, $4::uuid) RETURNING id::text`, acct, name, opportunityMotion(c.field("Type")), optional(owner)).Scan(&id)
	if err != nil {
		return fmt.Errorf("graph: create opportunity %s: %w", c.RecordID, err)
	}
	st.entitiesChanged = true
	_, err = st.ensureMapping(ctx, tx, Mapping{EntityType: EntityOpportunity, EntityID: id, SourceKey: CRMKey(c.RecordID),
		Method: MethodExact, Confidence: exactConfidence, ValidFrom: at})
	return err
}

// employeeByEmail returns the employee with that email, or "" (employees are only ever seeded).
func employeeByEmail(ctx context.Context, q DBTX, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", nil
	}
	var id string
	err := q.QueryRowContext(ctx, `
SELECT p.id::text FROM entity_source_mappings m JOIN people p ON p.id = m.entity_id
 WHERE m.entity_type = 'person' AND m.valid_to IS NULL AND m.source_system = 'email' AND m.source_key = $1 AND p.kind = 'employee'`, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("graph: employee by email: %w", err)
	}
	return id, nil
}

func prepareContact(ctx context.Context, tx Txn, st *prepared, c crmEvent, at time.Time) error {
	if err := lock(ctx, tx, "crm-record", c.RecordID); err != nil {
		return err
	}
	acct, err := entityByCRM(ctx, tx, c.accountRecord())
	if err != nil || acct == "" {
		return err
	}
	if _, ok, err := CurrentMapping(ctx, tx, CRMKey(c.RecordID)); err != nil || ok {
		return err
	}
	mail := strings.ToLower(c.field("Email"))
	if mail != "" && normalize.IsOurEmail(mail) {
		return nil // we never create people for our own domain; employees are seeded
	}
	name := strings.TrimSpace(c.field("FirstName") + " " + c.field("LastName"))
	person, err := findContact(ctx, tx, mail)
	if err != nil {
		return err
	}
	if person == "" {
		if name == "" {
			name = c.RecordID
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO people (kind, display_name, primary_email, title, account_id)
			VALUES ('contact', $1, $2, $3, $4::uuid) RETURNING id::text`, name, optional(mail), optional(c.field("Title")), acct).Scan(&person)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE people SET account_id = $2::uuid, display_name = COALESCE(NULLIF($3, ''), display_name),
			title = COALESCE(NULLIF($4, ''), title), updated_at = now() WHERE id = $1::uuid`, person, acct, name, c.field("Title"))
	}
	if err != nil {
		return fmt.Errorf("graph: create contact %s: %w", c.RecordID, err)
	}
	if _, err := st.ensureMapping(ctx, tx, Mapping{EntityType: EntityPerson, EntityID: person, SourceKey: CRMKey(c.RecordID),
		Method: MethodExact, Confidence: exactConfidence, ValidFrom: at}); err != nil {
		return err
	}
	if mail == "" {
		return nil
	}
	_, err = st.ensureMapping(ctx, tx, Mapping{EntityType: EntityPerson, EntityID: person, SourceKey: EmailKey(mail),
		Method: MethodExact, Confidence: exactConfidence, ValidFrom: at})
	return err
}

// findContact finds the contact that owns an email: through a current mapping, else a people
// row with that primary email. It serializes concurrent creators of the same address.
func findContact(ctx context.Context, q Txn, email string) (string, error) {
	if email == "" {
		return "", nil
	}
	if err := lock(ctx, q, "person-email", email); err != nil {
		return "", err
	}
	if m, ok, err := CurrentMapping(ctx, q, EmailKey(email)); err != nil || ok {
		return m.EntityID, err
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT id::text FROM people WHERE primary_email = $1 AND kind = 'contact'`, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("graph: contact by email: %w", err)
	}
	return id, nil
}
