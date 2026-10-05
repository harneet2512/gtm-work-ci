// Package strategytest seeds the HAR-129 demo episode (a run with a business-intelligence update, a strategy
// set of three complete candidates and their eval bundles) into a migrated database from the contract
// examples, with fresh ids per call. It is test support only; nothing in cmd/ imports it.
//
// The rows are what the orchestrator and the worker will write (HAR-117, HAR-119): this repository's
// endpoints only persist and serve them, so their tests start from seeded rows.
package strategytest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

// Seeded names what Seed wrote.
type Seeded struct {
	AccountID, RunID, EpisodeID, SetID, BIID string
	Candidates                               [3]string // ranked 1, 2, 3; the first is Ghost's preference
	Bundles                                  [3]string
	Marco, Priya                             string // people of the account (the candidates' recipients)
	ActivityID                               string
	// KnowledgeID is the candidate knowledge the rank-2 candidate's knowledge_refs point at (the
	// contract strategy set example links it): the supervision evidence the demo exercises lands here.
	KnowledgeID string
}

// Fixed ids inside the contract examples that Seed replaces.
const (
	exSet, exEpisode, exRun, exAccount = "05e70000-0000-4000-8000-000000000b01", "0e9e0000-0000-4000-8000-000000000a01", "0f0a0000-0000-4000-8000-000000000601", "0a0c0000-0000-4000-8000-000000000001"
	exMarco, exPriya                   = "0b0e0000-0000-4000-8000-000000000018", "0b0e0000-0000-4000-8000-000000000017"
	exBI, exChange                     = "0b100000-0000-4000-8000-000000000a01", "0acc0000-0000-4000-8000-000000000801"
	exActivity                         = "0ac70000-0000-4000-8000-000000000101"
	exInference, exDecision            = "01f00000-0000-4000-8000-000000000d01", "0d5d0000-0000-4000-8000-000000000c01"
	exKnowledge                        = "0c17c000-0000-4000-8000-000000000017"
)

var exCandidates = [3]string{"0ca00000-0000-4000-8000-0000000000a1", "0ca00000-0000-4000-8000-0000000000a2", "0ca00000-0000-4000-8000-0000000000a3"}

// Domain is the per-seed customer domain Marco's and Priya's addresses live on; an
// entity_source_mappings row ties it to the account, so mail ingested from them resolves to it
// (HAR-120: the supervision demo needs a reply that the real ingest can attribute).
func (s Seeded) Domain() string { return "customer-" + s.EpisodeID[:8] + ".example.test" }

// MarcoEmail / PriyaEmail are the seeded people's primary_email values.
func (s Seeded) MarcoEmail() string { return "marco-" + s.Marco[:8] + "@" + s.Domain() }
func (s Seeded) PriyaEmail() string { return "priya-" + s.Priya[:8] + "@" + s.Domain() }

// NewID returns a random version 4 UUID.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// ExampleFile reads contracts/examples/<name>.example.json, found by walking up from the working directory.
func ExampleFile(name string) ([]byte, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "contracts", "examples", name+".example.json"))
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		return nil, fmt.Errorf("strategytest: contracts/examples/%s.example.json not found", name)
	}
}

// Example reads contracts/examples/<name>.example.json, found by walking up from the working directory.
func Example(t testing.TB, name string) []byte {
	t.Helper()
	b, err := ExampleFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// freshRun cancels the account's open runs and inserts an awaiting run (ctxfixture.FreshRun without a TB).
func freshRun(db *sql.DB, accountID, status string) (string, error) {
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE agent_runs SET status = 'cancelled', updated_at = now()
 WHERE account_id = $1::uuid AND status IN ('pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited')`, accountID); err != nil {
		return "", fmt.Errorf("cancel open runs: %w", err)
	}
	runID, _, err := ctxfixture.InsertRun(ctx, db, accountID, status)
	return runID, err
}

// SeedE writes one awaiting-choice episode for accountID — the non-test form of Seed, for the real-core
// smoke backend (demosmoke): the account must have state and no open run that matters.
func SeedE(db *sql.DB, accountID string) (Seeded, error) {
	s := Seeded{AccountID: accountID, EpisodeID: NewID(), SetID: NewID(), BIID: NewID(), Marco: NewID(), Priya: NewID(), KnowledgeID: NewID()}
	runID, err := freshRun(db, accountID, "awaiting_human")
	if err != nil {
		return s, fmt.Errorf("strategytest: fresh run: %w", err)
	}
	s.RunID = runID
	for i := range s.Candidates {
		s.Candidates[i], s.Bundles[i] = NewID(), NewID()
	}
	if err := db.QueryRow(`SELECT trigger_activity_ids[1]::text FROM agent_runs WHERE id = $1::uuid`, s.RunID).Scan(&s.ActivityID); err != nil {
		return s, fmt.Errorf("strategytest: trigger activity: %w", err)
	}
	repl := map[string]string{exActivity: s.ActivityID, exSet: s.SetID, exEpisode: s.EpisodeID, exRun: s.RunID, exAccount: accountID, exMarco: s.Marco, exPriya: s.Priya, exBI: s.BIID, exChange: NewID(), exKnowledge: s.KnowledgeID}
	for i, c := range exCandidates {
		repl[c] = s.Candidates[i]
	}
	bi, err := ExampleFile("business_intelligence_update")
	if err != nil {
		return s, err
	}
	set, err := ExampleFile("strategy_set")
	if err != nil {
		return s, err
	}
	bundle, err := ExampleFile("eval_bundle")
	if err != nil {
		return s, err
	}
	ex := examples{
		bi:     remap(bi, repl),
		set:    remap(set, repl),
		bundle: remap(bundle, repl),
		change: repl[exChange],
	}
	if err := seedRows(db, &s, ex); err != nil {
		return s, fmt.Errorf("strategytest: seed: %w", err)
	}
	return s, nil
}

// Seed writes one awaiting-choice episode for accountID: the account must have state and no open run that
// matters (the account's open runs are cancelled, as ctxfixture.FreshRun does).
func Seed(t testing.TB, db *sql.DB, accountID string) Seeded {
	t.Helper()
	s, err := SeedE(db, accountID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func remap(raw []byte, repl map[string]string) []byte {
	text := string(raw)
	for from, to := range repl {
		text = strings.ReplaceAll(text, from, to)
	}
	return []byte(text)
}

type exec interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// examples are the contract examples with this seed's ids.
type examples struct {
	bi, set, bundle []byte
	change          string
}

func seedRows(db *sql.DB, s *Seeded, ex examples) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := seedWorld(tx, s, ex); err != nil {
		return err
	}
	return tx.Commit()
}

func seedWorld(tx *sql.Tx, s *Seeded, ex examples) error {
	var fromV, toV int
	if err := tx.QueryRow(`SELECT version FROM account_state WHERE account_id = $1::uuid`, s.AccountID).Scan(&fromV); err != nil {
		return fmt.Errorf("account state: %w", err)
	}
	if err := tx.QueryRow(`SELECT COALESCE(max(to_version), 6999) + 1 FROM state_diffs WHERE account_id = $1::uuid AND to_version >= 7000`, s.AccountID).Scan(&toV); err != nil {
		return err
	}
	diffID, changeID := NewID(), ex.change
	var plain []string
	plain = append(plain,
		// Recipients need an email: the send-time re-evaluation (HAR-139) blocks an email action whose
		// recipient has none. The addresses derive from the person ids so parallel seeds never collide.
		`INSERT INTO people (id, kind, display_name, account_id, primary_email) VALUES
		 ('`+s.Marco+`', 'contact', 'Marco (seed)', '`+s.AccountID+`', '`+s.MarcoEmail()+`'),
		 ('`+s.Priya+`', 'contact', 'Priya (seed)', '`+s.AccountID+`', '`+s.PriyaEmail()+`')`,
		// Identities a real ingest resolves: the customer domain names the account, each mailbox the
		// person — the supervision demo's reply then lands attributed and linked to its author.
		`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method) VALUES
		 ('account', '`+s.AccountID+`', 'domain', '`+s.Domain()+`', 1, 'seed'),
		 ('person', '`+s.Marco+`', 'email', '`+s.MarcoEmail()+`', 1, 'seed'),
		 ('person', '`+s.Priya+`', 'email', '`+s.PriyaEmail()+`', 1, 'seed')`,
		`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ('`+s.RunID+`',1,'strategy_generator','{}'),('`+s.RunID+`',2,'strategy_generator','{}'),('`+s.RunID+`',3,'strategy_generator','{}')`,
		`INSERT INTO state_history (account_id, version, as_of, state) SELECT account_id, `+fmt.Sprint(toV)+`, as_of, state FROM account_state WHERE account_id = '`+s.AccountID+`' ON CONFLICT DO NOTHING`)
	for _, q := range plain {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("%.60s: %w", q, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status) VALUES
 ($1::uuid,1,'build_context','dry_run','succeeded'),($1::uuid,2,'draft','dry_run','succeeded'),($1::uuid,3,'crm_intent','dry_run','succeeded'),
 ($1::uuid,4,'await_human','dry_run','succeeded'),($1::uuid,5,'execute','dry_run','pending')`, s.RunID); err != nil {
		return fmt.Errorf("steps: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO state_diffs (id, account_id, from_version, to_version, is_material, changes) VALUES ($1::uuid, $2::uuid, $3, $4, true,
 '[{"field":"stage","op":"changed","before":"a","after":"b","material":true}]'::jsonb)`, diffID, s.AccountID, fromV, toV); err != nil {
		return fmt.Errorf("state diff: %w", err)
	}
	ev := fmt.Sprintf(`[{"activity_id":%q}]`, s.ActivityID)
	if _, err := tx.Exec(`INSERT INTO account_changes (id, account_id, trigger_activity_ids, previous_state_ref, current_state_ref, state_diff_id, graph_diff_ref, material_change, evidence_refs)
 VALUES ($1::uuid, $2::uuid, ARRAY[$3::uuid], jsonb_build_object('account_id', $2::text, 'version', $4::int), jsonb_build_object('account_id', $2::text, 'version', $5::int), $6::uuid, '{}', true, $7::jsonb)`,
		changeID, s.AccountID, s.ActivityID, fromV, toV, diffID, ev); err != nil {
		return fmt.Errorf("account change: %w", err)
	}
	if err := seedBI(tx, s, ex.bi, changeID); err != nil {
		return err
	}
	if err := seedKnowledge(tx, s); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, status, account_change_id, business_intelligence_update_id)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'awaiting_choice', $5::uuid, $6::uuid)`, s.EpisodeID, s.RunID, s.AccountID, toV, changeID, s.BIID); err != nil {
		return fmt.Errorf("episode: %w", err)
	}
	return seedStrategies(tx, s, ex, toV, diffID)
}

// seedKnowledge inserts the contract knowledge example as a fresh candidate (no earned counts: the
// store refuses evidence nobody recorded). The strategy set example's rank-2 candidate references the
// example's id, which remap already pointed at s.KnowledgeID.
func seedKnowledge(tx *sql.Tx, s *Seeded) error {
	raw, err := ExampleFile("knowledge")
	if err != nil {
		return err
	}
	var k knowledge.Knowledge
	if err := json.Unmarshal(raw, &k); err != nil {
		return fmt.Errorf("knowledge example: %w", err)
	}
	k.ID = s.KnowledgeID
	k.Key = nil // the example's "K17" is taken on a second seed — let the sequence assign one
	k.Status = knowledge.StatusCandidate
	k.Counts = knowledge.Counts{}
	k.SupportingDecisionEpisodeIDs = nil
	k.Counterexamples = nil
	k.StatusHistory = nil
	k.LastValidatedAt = nil
	k.CreatedAt = time.Time{} // a live seed takes the database clock
	note := "seeded candidate; supervision evidence accrues from the demo send"
	k.Provenance = knowledge.Provenance{CreatedFrom: "seed_history", Note: &note}
	if _, err := knowledgestore.Insert(context.Background(), tx, k, "strategytest seed"); err != nil {
		return fmt.Errorf("knowledge: %w", err)
	}
	return nil
}

func seedBI(tx *sql.Tx, s *Seeded, raw []byte, changeID string) error {
	var bi map[string]json.RawMessage
	if err := json.Unmarshal(raw, &bi); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO business_intelligence_updates (id, account_id, account_change_id, summary, claims, why_it_matters, knowledge_refs, account_map_ref, model)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::jsonb, $6, '{}', $7::jsonb, 'seed')`,
		s.BIID, s.AccountID, changeID, unquote(bi["summary"]), string(bi["claims"]), unquote(bi["why_it_matters"]), string(bi["account_map_ref"]))
	if err != nil {
		return fmt.Errorf("business intelligence: %w", err)
	}
	return nil
}

func unquote(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}
