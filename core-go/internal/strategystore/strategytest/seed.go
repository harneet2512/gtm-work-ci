// Package strategytest seeds the HAR-129 demo episode (a run with a business-intelligence update, a strategy
// set of three complete candidates and their eval bundles) into a migrated database from the contract
// examples, with fresh ids per call. It is test support only; nothing in cmd/ imports it.
//
// The rows are what the orchestrator and the worker will write (HAR-117, HAR-119): this repository's
// endpoints only persist and serve them, so their tests start from seeded rows.
package strategytest

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
)

// Seeded names what Seed wrote.
type Seeded struct {
	AccountID, RunID, EpisodeID, SetID, BIID string
	Candidates                               [3]string // ranked 1, 2, 3; the first is Ghost's preference
	Bundles                                  [3]string
	Marco, Priya                             string // people of the account (the candidates' recipients)
	ActivityID                               string
}

// Fixed ids inside the contract examples that Seed replaces.
const (
	exSet, exEpisode, exRun, exAccount = "05e70000-0000-4000-8000-000000000b01", "0e9e0000-0000-4000-8000-000000000a01", "0f0a0000-0000-4000-8000-000000000601", "0a0c0000-0000-4000-8000-000000000001"
	exMarco, exPriya                   = "0b0e0000-0000-4000-8000-000000000018", "0b0e0000-0000-4000-8000-000000000017"
	exBI, exChange                     = "0b100000-0000-4000-8000-000000000a01", "0acc0000-0000-4000-8000-000000000801"
	exActivity                         = "0ac70000-0000-4000-8000-000000000101"
	exInference, exDecision            = "01f00000-0000-4000-8000-000000000d01", "0d5d0000-0000-4000-8000-000000000c01"
)

var exCandidates = [3]string{"0ca00000-0000-4000-8000-0000000000a1", "0ca00000-0000-4000-8000-0000000000a2", "0ca00000-0000-4000-8000-0000000000a3"}

// NewID returns a random version 4 UUID.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Example reads contracts/examples/<name>.example.json, found by walking up from the working directory.
func Example(t testing.TB, name string) []byte {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "contracts", "examples", name+".example.json"))
		if err == nil {
			return b
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		t.Fatalf("strategytest: contracts/examples/%s.example.json not found", name)
	}
}

// Seed writes one awaiting-choice episode for accountID: the account must have state and no open run that
// matters (the account's open runs are cancelled, as ctxfixture.FreshRun does).
func Seed(t testing.TB, db *sql.DB, accountID string) Seeded {
	t.Helper()
	s := Seeded{AccountID: accountID, EpisodeID: NewID(), SetID: NewID(), BIID: NewID(), Marco: NewID(), Priya: NewID()}
	s.RunID = ctxfixture.FreshRun(t, db, accountID, "awaiting_human")
	for i := range s.Candidates {
		s.Candidates[i], s.Bundles[i] = NewID(), NewID()
	}
	if err := db.QueryRow(`SELECT trigger_activity_ids[1]::text FROM agent_runs WHERE id = $1::uuid`, s.RunID).Scan(&s.ActivityID); err != nil {
		t.Fatalf("strategytest: trigger activity: %v", err)
	}
	repl := map[string]string{exActivity: s.ActivityID, exSet: s.SetID, exEpisode: s.EpisodeID, exRun: s.RunID, exAccount: accountID, exMarco: s.Marco, exPriya: s.Priya, exBI: s.BIID, exChange: NewID()}
	for i, c := range exCandidates {
		repl[c] = s.Candidates[i]
	}
	ex := examples{
		bi:     remap(Example(t, "business_intelligence_update"), repl),
		set:    remap(Example(t, "strategy_set"), repl),
		bundle: remap(Example(t, "eval_bundle"), repl),
		change: repl[exChange],
	}
	if err := seedRows(db, &s, ex); err != nil {
		t.Fatalf("strategytest: seed: %v", err)
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

func seedWorld(tx exec, s *Seeded, ex examples) error {
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
		`INSERT INTO people (id, kind, display_name, account_id) VALUES ('`+s.Marco+`', 'contact', 'Marco (seed)', '`+s.AccountID+`'), ('`+s.Priya+`', 'contact', 'Priya (seed)', '`+s.AccountID+`')`,
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
	if _, err := tx.Exec(`INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, status, account_change_id, business_intelligence_update_id)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'awaiting_choice', $5::uuid, $6::uuid)`, s.EpisodeID, s.RunID, s.AccountID, toV, changeID, s.BIID); err != nil {
		return fmt.Errorf("episode: %w", err)
	}
	return seedStrategies(tx, s, ex, toV, diffID)
}

func seedBI(tx exec, s *Seeded, raw []byte, changeID string) error {
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
