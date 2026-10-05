package graph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// participant is one activity_participants row with what resolution learned about it.
type participant struct {
	raw      string
	key      SourceKey
	role     string
	name     string
	personID string
	conf     float64 // confidence of the identity mapping behind personID
	kind     string  // 'employee' or 'contact' once resolved
}

// finalizer carries the state of one Finalize call.
type finalizer struct {
	tx    Txn
	in    ingest.FinalizeInput
	at    time.Time
	parts []participant
}

func (f *finalizer) loadParticipants(ctx context.Context) error {
	rows, err := f.tx.QueryContext(ctx, `
SELECT raw_identity, role, coalesce(display_name, ''), coalesce(person_id::text, '')
  FROM activity_participants WHERE activity_id = $1::uuid ORDER BY role, raw_identity`, f.in.ActivityID)
	if err != nil {
		return fmt.Errorf("graph: load participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p participant
		if err := rows.Scan(&p.raw, &p.role, &p.name, &p.personID); err != nil {
			return fmt.Errorf("graph: scan participant: %w", err)
		}
		p.key = SourceKeyOf(p.raw)
		f.parts = append(f.parts, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graph: read participants: %w", err)
	}
	return nil
}

// foundMapping is the person a source identity currently maps to.
type foundMapping struct {
	id   string
	conf float64
}

// syncParticipants brings the participants in line with the current mappings in a fixed number
// of statements: one lookup of every identity, one batched link of the rows that changed, one
// lookup of the people's kinds.
func (f *finalizer) syncParticipants(ctx context.Context) error {
	mapped, err := f.lookupPersonMappings(ctx)
	if err != nil {
		return err
	}
	var raws, roles, ids []string
	for i := range f.parts {
		p := &f.parts[i]
		m, ok := mapped[p.key]
		if !ok {
			continue
		}
		if p.personID != m.id {
			raws, roles, ids = append(raws, p.raw), append(roles, p.role), append(ids, m.id)
		}
		p.personID, p.conf = m.id, m.conf
	}
	if len(raws) > 0 {
		_, err := f.tx.ExecContext(ctx, `
UPDATE activity_participants ap SET person_id = t.pid::uuid
  FROM unnest($2::text[], $3::text[], $4::text[]) AS t(raw, role, pid)
 WHERE ap.activity_id = $1::uuid AND ap.raw_identity = t.raw AND ap.role = t.role`, f.in.ActivityID, raws, roles, ids)
		if err != nil {
			return fmt.Errorf("graph: link participants: %w", err)
		}
	}
	return f.loadKinds(ctx)
}

// lookupPersonMappings finds the current person mapping of every participant identity in one query.
func (f *finalizer) lookupPersonMappings(ctx context.Context) (map[SourceKey]foundMapping, error) {
	var systems, keys []string
	for _, p := range f.parts {
		if !p.key.IsZero() {
			systems, keys = append(systems, p.key.System), append(keys, p.key.Key)
		}
	}
	mapped := map[SourceKey]foundMapping{}
	if len(keys) == 0 {
		return mapped, nil
	}
	rows, err := f.tx.QueryContext(ctx, `
SELECT m.source_system, m.source_key, m.entity_id::text, m.confidence::float8
  FROM unnest($1::text[], $2::text[]) AS t(sys, key)
  JOIN entity_source_mappings m ON m.source_system = t.sys AND m.source_key = t.key
 WHERE m.valid_to IS NULL AND m.entity_type = 'person'`, systems, keys)
	if err != nil {
		return nil, fmt.Errorf("graph: look up participant identities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k SourceKey
		var fnd foundMapping
		if err := rows.Scan(&k.System, &k.Key, &fnd.id, &fnd.conf); err != nil {
			return nil, fmt.Errorf("graph: scan participant identity: %w", err)
		}
		mapped[k] = fnd
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read participant identities: %w", err)
	}
	return mapped, nil
}

func (f *finalizer) loadKinds(ctx context.Context) error {
	var people []string
	for _, p := range f.parts {
		if p.personID != "" {
			people = append(people, p.personID)
		}
	}
	if len(people) == 0 {
		return nil
	}
	rows, err := f.tx.QueryContext(ctx, `SELECT id::text, kind FROM people WHERE id = ANY($1::uuid[])`, people)
	if err != nil {
		return fmt.Errorf("graph: load participant kinds: %w", err)
	}
	defer rows.Close()
	kinds := map[string]string{}
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return fmt.Errorf("graph: scan participant kind: %w", err)
		}
		kinds[id] = kind
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graph: read participant kinds: %w", err)
	}
	for i := range f.parts {
		f.parts[i].kind = kinds[f.parts[i].personID]
	}
	return nil
}

// createMissingContacts makes a contact for every unresolved email participant that may have
// one created (see createContactByRule).
func (f *finalizer) createMissingContacts(ctx context.Context) error {
	for i := range f.parts {
		p := &f.parts[i]
		if p.personID != "" || p.key.System != "email" || !takesPart(p.role) {
			continue
		}
		id, err := f.createContactByRule(ctx, p.key.Key, p.name)
		if err != nil {
			return err
		}
		if id != "" {
			p.personID, p.conf = id, ruleConfidence
		}
	}
	return nil
}

// createContactByRule makes a contact for an unknown external address whose domain belongs to
// an account (method rule, confidence 0.9, works_at at first_party_record). Our own domain,
// personal mail domains and domains without an account yield nothing.
func (f *finalizer) createContactByRule(ctx context.Context, email, name string) (string, error) {
	if normalize.IsOurEmail(email) {
		return "", nil
	}
	_, domain, _ := strings.Cut(email, "@")
	acct, err := accountByDomain(ctx, f.tx, domain)
	if err != nil || acct == "" {
		return "", err
	}
	id, err := findContact(ctx, f.tx, email)
	if err != nil {
		return "", err
	}
	if id == "" {
		if id, err = f.insertContact(ctx, email, name, acct); err != nil {
			return "", err
		}
	}
	if _, created, err := EnsureMapping(ctx, f.tx, Mapping{EntityType: EntityPerson, EntityID: id, SourceKey: EmailKey(email),
		Confidence: ruleConfidence, Method: MethodRule, EvidenceActivityID: f.in.ActivityID, ValidFrom: f.at,
		Evidence: evidenceJSON(map[string]string{"rule": "email_domain_account", "domain": domain})}); err != nil {
		return "", err
	} else if created {
		if err := f.repoint(ctx, EmailKey(email), id, ruleConfidence); err != nil {
			return "", err
		}
	}
	_, err = UpsertEdge(ctx, f.tx, EdgeSpec{Src: EntityRef{EntityPerson, id}, Rel: RelWorksAt, Dst: EntityRef{EntityAccount, acct},
		Standing: StandingFirstPartyRecord, Confidence: ruleConfidence, SourceActivityID: f.in.ActivityID,
		Evidence: evidenceJSON(map[string]string{"rule": "email_domain_account", "domain": domain}), ValidFrom: f.at})
	return id, err
}

func (f *finalizer) insertContact(ctx context.Context, email, name, acct string) (string, error) {
	if name = strings.TrimSpace(name); name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	var id string
	err := f.tx.QueryRowContext(ctx, `INSERT INTO people (kind, display_name, primary_email, account_id)
		VALUES ('contact', $1, $2, $3::uuid) RETURNING id::text`, name, email, acct).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("graph: create contact %s: %w", email, err)
	}
	return id, nil
}

// newMapping opens a mapping evidenced by this activity and re-points the participant rows
// that carry the identity.
func (f *finalizer) newMapping(ctx context.Context, m Mapping) error {
	m.EvidenceActivityID, m.ValidFrom = f.in.ActivityID, f.at
	if _, err := InsertMapping(ctx, f.tx, m); err != nil {
		return err
	}
	return f.repoint(ctx, m.SourceKey, m.EntityID, m.Confidence)
}

// movedRow is a participant row that repoint just linked to a person.
type movedRow struct {
	role string
	act  actInfo
}

// repoint points every participant row carrying the identity at the person, including rows of
// past activities (a mapping only changes by remap; the identity's history lives in
// entity_source_mappings). Each past activity gets the edges the new link implies, and its
// account a recompute, because its picture of who took part has changed.
func (f *finalizer) repoint(ctx context.Context, k SourceKey, personID string, conf float64) error {
	moved, err := f.moveParticipantRows(ctx, k, personID)
	if err != nil {
		return err
	}
	for i := range f.parts {
		if f.parts[i].key == k {
			f.parts[i].personID, f.parts[i].conf = personID, conf
		}
	}
	kind, err := personKind(ctx, f.tx, personID)
	if err != nil {
		return err
	}
	for _, r := range moved {
		if r.act.id == f.in.ActivityID {
			continue // this activity's edges and recompute follow in Finalize
		}
		if err := participantEdges(ctx, f.tx, r.act, participant{personID: personID, kind: kind, role: r.role, conf: conf}); err != nil {
			return err
		}
		if r.act.accountID != "" && f.in.EnqueueRecompute != nil {
			if err := f.in.EnqueueRecompute(ctx, r.act.accountID, r.act.id); err != nil {
				return fmt.Errorf("graph: enqueue recompute after re-pointing: %w", err)
			}
		}
	}
	return nil
}

// moveParticipantRows relinks the rows and returns what the edge rules need about each one.
func (f *finalizer) moveParticipantRows(ctx context.Context, k SourceKey, personID string) ([]movedRow, error) {
	rows, err := f.tx.QueryContext(ctx, `
WITH moved AS (
    UPDATE activity_participants SET person_id = $2::uuid
     WHERE raw_identity = $1 AND person_id IS DISTINCT FROM $2::uuid RETURNING activity_id, role)
SELECT m.activity_id::text, m.role, a.activity_type, a.occurred_at, coalesce(a.account_id::text, ''),
       coalesce(a.opportunity_id::text, ''), coalesce(o.owner_person_id::text, '')
  FROM moved m JOIN activities a ON a.id = m.activity_id LEFT JOIN opportunities o ON o.id = a.opportunity_id`,
		k.Raw(), personID)
	if err != nil {
		return nil, fmt.Errorf("graph: re-point participants: %w", err)
	}
	defer rows.Close()
	var out []movedRow
	for rows.Next() {
		var r movedRow
		if err := rows.Scan(&r.act.id, &r.role, &r.act.typ, &r.act.at, &r.act.accountID, &r.act.oppID, &r.act.ownerID); err != nil {
			return nil, fmt.Errorf("graph: scan re-pointed participant: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read re-pointed participants: %w", err)
	}
	return out, nil
}

func personKind(ctx context.Context, q DBTX, personID string) (string, error) {
	var kind string
	err := q.QueryRowContext(ctx, `SELECT kind FROM people WHERE id = $1::uuid`, personID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("graph: person kind: %w", err)
	}
	return kind, nil
}
