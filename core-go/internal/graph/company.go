package graph

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Company is the vendor's own company (fixtures/world/org.json): employees only. Accounts and
// their contacts are never listed here; they are discovered from events.
type Company struct {
	Organization CompanyInfo     `json:"organization"`
	People       []CompanyPerson `json:"people"`
	// Source is the file the company was loaded from; seeded mappings and edges record it.
	Source string `json:"-"`
}

// CompanyInfo names the vendor.
type CompanyInfo struct {
	Name    string `json:"name"`
	Domain  string `json:"domain"`
	Product string `json:"product"`
}

// CompanyPerson is one employee.
type CompanyPerson struct {
	Key         string  `json:"key"`
	Kind        string  `json:"kind"`
	DisplayName string  `json:"display_name"`
	Email       string  `json:"email"`
	Title       string  `json:"title"`
	Role        string  `json:"role"`
	SlackUser   string  `json:"slack_user"`
	ManagerKey  *string `json:"manager_key"`
}

// SeedReport counts what a seed run changed.
type SeedReport struct {
	Created int
	Updated int
}

// LoadCompany reads a company file, rejecting unknown fields.
func LoadCompany(path string) (Company, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Company{}, fmt.Errorf("graph: read company file: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Company
	if err := dec.Decode(&c); err != nil {
		return Company{}, fmt.Errorf("graph: decode company file %s: %w", path, err)
	}
	c.Source = path
	return c, nil
}

// normalized validates the file as a whole and returns a cleaned copy (lower-cased emails,
// trimmed names); the receiver is left untouched. A bad file seeds nothing.
func (c Company) normalized() (Company, error) {
	if d := strings.ToLower(strings.TrimSpace(c.Organization.Domain)); d != "" && d != normalize.OurDomain {
		return Company{}, fmt.Errorf("graph: company domain %q is not our domain %q", c.Organization.Domain, normalize.OurDomain)
	}
	if len(c.People) == 0 {
		return Company{}, errors.New("graph: company file lists no people")
	}
	out := c
	out.People = make([]CompanyPerson, len(c.People))
	keys, emails := map[string]bool{}, map[string]bool{}
	for i, p := range c.People {
		p.Email = strings.ToLower(strings.TrimSpace(p.Email))
		p.DisplayName = strings.TrimSpace(p.DisplayName)
		switch {
		case p.Kind != "employee":
			return Company{}, fmt.Errorf("graph: company person %q must have kind employee", p.Key)
		case p.Key == "" || p.DisplayName == "":
			return Company{}, fmt.Errorf("graph: company person %q needs a key and a display name", p.Key)
		case !normalize.IsOurEmail(p.Email):
			return Company{}, fmt.Errorf("graph: company person %q email %q is not an @%s address", p.Key, p.Email, normalize.OurDomain)
		case keys[p.Key]:
			return Company{}, fmt.Errorf("graph: duplicate company key %q", p.Key)
		case emails[p.Email]:
			return Company{}, fmt.Errorf("graph: duplicate company email %q", p.Email)
		}
		keys[p.Key], emails[p.Email] = true, true
		out.People[i] = p
	}
	for _, p := range out.People {
		if p.ManagerKey == nil {
			continue
		}
		if *p.ManagerKey == p.Key || !keys[*p.ManagerKey] {
			return Company{}, fmt.Errorf("graph: company person %q has an invalid manager %q", p.Key, *p.ManagerKey)
		}
	}
	return out, nil
}

// SeedCompany upserts the employees, their email and Slack identities (method seed) and their
// reports_to edges in one transaction. It is idempotent; changes close the old mapping or edge
// at `at` and open a new one. Mappings and edges record the file they came from.
func SeedCompany(ctx context.Context, db *sql.DB, company Company, at time.Time) (SeedReport, error) {
	c, err := company.normalized()
	if err != nil {
		return SeedReport{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return SeedReport{}, fmt.Errorf("graph: begin seed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	s := seeder{tx: tx, at: at, evidence: evidenceJSON(map[string]string{"source": c.Source})}
	ids := map[string]string{}
	created, changed := map[string]bool{}, map[string]bool{}
	for _, p := range c.People {
		id, isNew, updated, err := s.person(ctx, p)
		if err != nil {
			return SeedReport{}, err
		}
		ids[p.Key], created[p.Key], changed[p.Key] = id, isNew, updated
	}
	for _, p := range c.People {
		moved, err := s.manager(ctx, p, ids)
		if err != nil {
			return SeedReport{}, err
		}
		changed[p.Key] = changed[p.Key] || moved
	}
	var rep SeedReport
	for _, p := range c.People {
		switch {
		case created[p.Key]:
			rep.Created++
		case changed[p.Key]:
			rep.Updated++
		}
	}
	if err := tx.Commit(); err != nil {
		return SeedReport{}, fmt.Errorf("graph: commit seed: %w", err)
	}
	return rep, nil
}

type seeder struct {
	tx       Txn
	at       time.Time
	evidence json.RawMessage
}

func (s seeder) mapping(id string, k SourceKey) Mapping {
	return Mapping{EntityType: EntityPerson, EntityID: id, SourceKey: k, Confidence: exactConfidence, Method: MethodSeed, Evidence: s.evidence, ValidFrom: s.at}
}

func (s seeder) person(ctx context.Context, p CompanyPerson) (id string, created, updated bool, err error) {
	m, ok, err := CurrentMapping(ctx, s.tx, EmailKey(p.Email))
	if err != nil {
		return "", false, false, err
	}
	if ok {
		if err = requireEmployee(ctx, s.tx, m, p.Email); err != nil {
			return "", false, false, err
		}
		id = m.EntityID
		updated, err = updateEmployee(ctx, s.tx, id, p)
	} else {
		id, created, err = findOrCreateEmployee(ctx, s.tx, p)
	}
	if err != nil {
		return "", false, false, err
	}
	if _, _, err := EnsureMapping(ctx, s.tx, s.mapping(id, EmailKey(p.Email))); err != nil {
		return "", false, false, err
	}
	slackChanged, err := s.slack(ctx, id, p.SlackUser)
	return id, created, updated || slackChanged, err
}

// requireEmployee fails unless the mapping points at an employee: an email that is already
// mapped to a contact, or to something that is not a person, must never be seeded as ours.
func requireEmployee(ctx context.Context, q DBTX, m Mapping, email string) error {
	if m.EntityType != EntityPerson {
		return fmt.Errorf("graph: %s is mapped to a %s, not an employee; refusing to seed it", email, m.EntityType)
	}
	var kind string
	if err := q.QueryRowContext(ctx, `SELECT kind FROM people WHERE id = $1::uuid`, m.EntityID).Scan(&kind); err != nil {
		return fmt.Errorf("graph: look up mapped person of %s: %w", email, err)
	}
	if kind != "employee" {
		return fmt.Errorf("graph: %s is mapped to a %s, not an employee; refusing to seed it", email, kind)
	}
	return nil
}

// findOrCreateEmployee returns the employee row for the email, creating it when there is none.
// An email that belongs to a contact is an error: employees and contacts never share an address.
func findOrCreateEmployee(ctx context.Context, tx DBTX, p CompanyPerson) (string, bool, error) {
	var id, kind string
	err := tx.QueryRowContext(ctx, `SELECT id::text, kind FROM people WHERE primary_email = $1`, p.Email).Scan(&id, &kind)
	switch {
	case err == nil && kind == "employee":
		return id, false, nil
	case err == nil:
		return "", false, fmt.Errorf("graph: %s is a %s, not an employee; refusing to seed it as one", p.Email, kind)
	case !errors.Is(err, sql.ErrNoRows):
		return "", false, fmt.Errorf("graph: look up employee %s: %w", p.Email, err)
	}
	err = tx.QueryRowContext(ctx, `
INSERT INTO people (kind, display_name, primary_email, title) VALUES ('employee', $1, $2, NULLIF($3, '')) RETURNING id::text`,
		p.DisplayName, p.Email, p.Title).Scan(&id)
	if err != nil {
		return "", false, fmt.Errorf("graph: create employee %s: %w", p.Email, err)
	}
	return id, true, nil
}

func updateEmployee(ctx context.Context, tx DBTX, id string, p CompanyPerson) (bool, error) {
	res, err := tx.ExecContext(ctx, `
UPDATE people SET display_name = $2, title = NULLIF($3, ''), updated_at = now()
 WHERE id = $1::uuid AND kind = 'employee' AND (display_name IS DISTINCT FROM $2 OR title IS DISTINCT FROM NULLIF($3, ''))`, id, p.DisplayName, p.Title)
	if err != nil {
		return false, fmt.Errorf("graph: update employee %s: %w", p.Email, err)
	}
	n, err := rowsAffected(res, "update employee")
	return n > 0, err
}

// slack makes slackUser the person's only current Slack identity.
func (s seeder) slack(ctx context.Context, personID, slackUser string) (bool, error) {
	stale, err := currentSlackMappings(ctx, s.tx, personID, slackUser)
	if err != nil {
		return false, err
	}
	for _, id := range stale {
		if err := CloseMapping(ctx, s.tx, id, s.at); err != nil {
			return false, err
		}
	}
	changed := len(stale) > 0
	if slackUser == "" {
		return changed, nil
	}
	k := SourceKey{System: "slack", Key: slackUser}
	cur, ok, err := CurrentMapping(ctx, s.tx, k)
	switch {
	case err != nil:
		return false, err
	case ok && cur.EntityID != personID:
		_, err = Remap(ctx, s.tx, k, personID, s.at, &MappingOverride{Method: MethodSeed, Evidence: s.evidence})
		return true, err
	case ok:
		return changed, nil
	}
	_, err = InsertMapping(ctx, s.tx, s.mapping(personID, k))
	return true, err
}

// currentSlackMappings lists the person's open Slack mappings other than keep.
func currentSlackMappings(ctx context.Context, q DBTX, personID, keep string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id::text FROM entity_source_mappings
 WHERE entity_type = 'person' AND entity_id = $1::uuid AND source_system = 'slack' AND valid_to IS NULL AND source_key <> $2`,
		personID, keep)
	if err != nil {
		return nil, fmt.Errorf("graph: list slack identities: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("graph: scan slack identity: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read slack identities: %w", err)
	}
	return ids, nil
}

// manager makes the person's manager (or none) their only open reports_to edge. The reporting chart
// is a deterministic first-party record (standing first_party_record).
func (s seeder) manager(ctx context.Context, p CompanyPerson, ids map[string]string) (bool, error) {
	src := EntityRef{Type: EntityPerson, ID: ids[p.Key]}
	var keep *EntityRef
	if p.ManagerKey != nil {
		keep = &EntityRef{Type: EntityPerson, ID: ids[*p.ManagerKey]}
	}
	closed, err := CloseEdges(ctx, s.tx, EdgeFilter{Src: &src, NotDst: keep, Rels: []string{RelReportsTo}}, s.at)
	if err != nil || keep == nil {
		return closed > 0, err
	}
	created, err := UpsertEdge(ctx, s.tx, EdgeSpec{Src: src, Rel: RelReportsTo, Dst: *keep, Standing: StandingFirstPartyRecord,
		Confidence: exactConfidence, Evidence: s.evidence, ValidFrom: s.at})
	return closed > 0 || created, err
}
