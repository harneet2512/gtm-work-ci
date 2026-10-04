package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const latestVersion = 27

// SQLSTATE codes asserted by the constraint tests.
const (
	uniqueViolation     = "23505"
	checkViolation      = "23514"
	foreignKeyViolation = "23503"
	notNullViolation    = "23502"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

func TestMigrationsReachLatestAndAreReversible(t *testing.T) {
	ctx := context.Background()
	assertVersion(t, ctx, latestVersion)

	if err := env.Migrator.DownTo(ctx, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	assertVersion(t, ctx, 0)

	if err := env.Migrator.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	assertVersion(t, ctx, latestVersion)
}

func TestHAR96TablesExist(t *testing.T) {
	// HAR-96 §13 table list plus the tables the design adds (see docs/linear-traceability.md).
	want := []string{
		"source_events", "activities", "activity_participants", "entity_source_mappings", "accounts",
		"people", "opportunities", "relationships", "account_state", "state_history", "state_diffs",
		"signals", "agent_runs", "eval_runs", "human_decisions", "customer_reactions",
		"business_outcomes", "knowledge", "knowledge_evidence", "rep_profiles",
		"claims", "recompute_jobs", "unresolved_activities", "trigger_evaluations", "opportunity_state", "opportunity_state_history",
		"agent_run_steps", "context_access_log", "extraction_cache",
		// HAR-97 (migration 0007)
		"agent_run_drafts", "evaluator_versions", "decision_guidance", "decision_episodes",
		"human_deltas", "human_delta_explanations", "knowledge_status_history", "eval_cases",
		// HAR-126 (migration 0021)
		"state_transitions", "state_transition_history",
	}
	for _, table := range want {
		var exists bool
		err := env.DB.QueryRow(`SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("lookup %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s missing", table)
		}
	}
}

type constraintCase struct {
	name     string
	sql      string
	wantCode string
}

var constraintCases = []constraintCase{
	// HAR-96 §4 idempotency
	{"duplicate delivery cannot create a second source event",
		`INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload) VALUES ('email','m1','received',repeat('f',64),'{}')`,
		uniqueViolation},
	{"unknown source system is rejected",
		`INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload) VALUES ('fax','m9','received',repeat('9',64),'{}')`,
		checkViolation},
	// §2 canonical activity families
	{"unknown activity type is rejected",
		`INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, provenance) VALUES ($SE2,'Telepathy','email','m2',now(),'{}')`,
		checkViolation},
	// §5 correct entity links
	{"activity opportunity must belong to the activity account",
		`INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, provenance) VALUES ($SE2,'EmailReceived','email','m2',now(),$OTHER_ACCOUNT,$OPP,'{}')`,
		foreignKeyViolation},
	{"relationship cannot point at itself",
		`INSERT INTO relationships (src_type, src_id, rel_type, dst_type, dst_id, standing, confidence, valid_from) VALUES ('account',$ACCOUNT,'belongs_to','account',$ACCOUNT,'crm_explicit',1,now())`,
		checkViolation},
	{"employees never belong to a customer account",
		`INSERT INTO people (kind, display_name, account_id) VALUES ('employee','Rep',$ACCOUNT)`,
		checkViolation},
	// §18 provenance and standing
	{"AI claim without verbatim evidence is rejected",
		`INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, occurred_at, extractor) VALUES ($ACCOUNT,'blockers','"x"','first_party_ai',0.9,$ACTIVITY,now(),'llm:test')`,
		checkViolation},
	{"first-party record claim must come from a rule (ADR-0009)",
		`INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, occurred_at, extractor) VALUES ($ACCOUNT,'next_meeting','null','first_party_record',1,$ACTIVITY,now(),'llm:test')`,
		checkViolation},
	{"same extractor cannot assert the same claim twice",
		`INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, occurred_at, extractor) VALUES ($ACCOUNT,'stage','"Technical evaluation"','crm_explicit',1,$ACTIVITY,now(),'rule:crm@1'), ($ACCOUNT,'stage','"Technical evaluation"','crm_explicit',0.5,$ACTIVITY,now(),'rule:crm@1')`,
		uniqueViolation},
	// §8 state diff
	{"material diff must list changes",
		`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($ACCOUNT,1,now(),'{}'); INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes) VALUES ($ACCOUNT,0,1,true,'[]')`,
		checkViolation},
	// §10 eligibility
	{"eligible evaluation cannot carry an ineligible reason",
		`INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ($ACCOUNT,'post_interaction_followup',true,'{no_material_change}')`,
		checkViolation},
	{"ineligible evaluation cannot carry an eligible reason",
		`INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ($ACCOUNT,'post_interaction_followup',false,'{eligible_customer_replied}')`,
		checkViolation},
	{"trigger evaluation needs at least one reason code",
		`INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ($ACCOUNT,'post_interaction_followup',false,'{}')`,
		checkViolation},
	{"unknown reason code is rejected",
		`INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ($ACCOUNT,'post_interaction_followup',false,'{felt_like_it}')`,
		checkViolation},
	{"ineligible evaluation cannot spawn a run",
		`INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids) VALUES ($ACCOUNT,'post_interaction_followup','dry_run','rejected',$INELIGIBLE,ARRAY[$ACTIVITY]::uuid[])`,
		foreignKeyViolation},
	{"one evaluation spawns at most one run",
		`UPDATE agent_runs SET status = 'rejected' WHERE id = $RUN; INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids) VALUES ($ACCOUNT,'post_interaction_followup','dry_run','pending',$TRIGGER,ARRAY[$ACTIVITY]::uuid[])`,
		uniqueViolation},
	{"only one open run per account and workflow",
		`INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids) VALUES ($ACCOUNT,'post_interaction_followup','dry_run','pending',$TRIGGER2,ARRAY[$ACTIVITY]::uuid[])`,
		uniqueViolation},
	// §18 dry-run cannot write externally
	{"dry-run run cannot reach executed",
		`UPDATE agent_runs SET status = 'executed' WHERE id = $RUN`,
		checkViolation},
	{"dry-run step cannot record an external effect",
		`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status, external_effect_id) VALUES ($RUN,9,'execute','dry_run','recorded','gmail:msg-123')`,
		checkViolation},
	{"step cannot claim live mode under a dry run",
		`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status, external_effect_id) VALUES ($RUN,9,'execute','live','succeeded','gmail:msg-123')`,
		foreignKeyViolation},
	{"dry-run execute step can only be recorded",
		`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status) VALUES ($RUN,9,'execute','dry_run','succeeded')`,
		checkViolation},
	// §12 human decisions (same object for web and Slack)
	{"edit decision requires the edited artifact",
		`INSERT INTO human_decisions (agent_run_id, decision, surface, actor_label) VALUES ($RUN,'edit','web','Dana')`,
		checkViolation},
	{"second decision on the same run is rejected",
		`INSERT INTO human_decisions (agent_run_id, decision, surface, actor_label) VALUES ($RUN,'approve','web','Dana'), ($RUN,'approve','slack','Dana')`,
		uniqueViolation},
	// audit trail
	{"accounts with history cannot be hard-deleted",
		`DELETE FROM accounts WHERE id = $ACCOUNT`,
		foreignKeyViolation},
	// §4 coalescing
	{"at most one pending recompute job per account",
		`INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at) VALUES ($ACCOUNT,now(),now()), ($ACCOUNT,now(),now())`,
		uniqueViolation},
}

func TestSchemaConstraints(t *testing.T) {
	for _, tc := range constraintCases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				err := execExpectingError(tx, ids.expand(tc.sql))
				assertSQLState(t, err, tc.wantCode)
			})
		})
	}
}

func TestRecomputeOutboxAllowsPendingWhileInFlight(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		// A worker holds the in-flight job while new activity arrives for the same account.
		_, err := tx.Exec(ids.expand(`
			INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids, claimed_at, claimed_by, lease_expires_at)
			VALUES ($ACCOUNT, now(), now(), ARRAY[$ACTIVITY]::uuid[], now(), 'w1', now() + interval '30 seconds');
			INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids)
			VALUES ($ACCOUNT, now() + interval '3 seconds', now(), ARRAY[$ACTIVITY]::uuid[]);`))
		if err != nil {
			t.Fatalf("pending and in-flight jobs for one account must coexist: %v", err)
		}
		// A claim without a lease cannot exist (crash recovery depends on the lease).
		err = execExpectingError(tx, ids.expand(`UPDATE recompute_jobs SET lease_expires_at = NULL WHERE claimed_at IS NOT NULL`))
		assertSQLState(t, err, checkViolation)
	})
}

func TestClaimStandingRankOrdersHumanOverCRMOverRecordOverAIOverThirdParty(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		for _, s := range []string{"third_party", "first_party_ai", "human_approved", "first_party_record", "crm_explicit"} {
			_, err := tx.Exec(ids.expand(`INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
				VALUES ($ACCOUNT,'stage',to_jsonb($1::text),$1,0.9,$ACTIVITY,'quote',now(),'rule:test@1')`), s)
			if err != nil {
				t.Fatalf("insert %s claim: %v", s, err)
			}
		}
		rows, err := tx.Query(ids.expand(`SELECT standing FROM claims WHERE account_id = $ACCOUNT ORDER BY standing_rank DESC`))
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got = append(got, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		want := "human_approved,crm_explicit,first_party_record,first_party_ai,third_party"
		if strings.Join(got, ",") != want {
			t.Fatalf("standing order = %v, want %s", got, want)
		}
	})
}

// seededIDs holds the ids of a minimal connected graph used by constraint tests.
type seededIDs map[string]string

// expand substitutes $KEY placeholders, longest key first so $SE never matches inside $SE2.
func (s seededIDs) expand(q string) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		q = strings.ReplaceAll(q, "$"+k, "'"+s[k]+"'::uuid")
	}
	return q
}

func seedGraph(t *testing.T, tx *sql.Tx) seededIDs {
	t.Helper()
	ids := seededIDs{}
	steps := []struct{ key, sql string }{
		{"ACCOUNT", `INSERT INTO accounts (name, domain) VALUES ('Acme Corp','acme.com') RETURNING id`},
		{"OTHER_ACCOUNT", `INSERT INTO accounts (name, domain) VALUES ('Beta Inc','beta.io') RETURNING id`},
		{"OPP", `INSERT INTO opportunities (account_id, name, motion) VALUES ($ACCOUNT,'Acme EU expansion','expansion') RETURNING id`},
		{"SE", `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload) VALUES ('email','m1','received',repeat('a',64),'{}') RETURNING id`},
		{"SE2", `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload) VALUES ('email','m2','received',repeat('b',64),'{}') RETURNING id`},
		{"ACTIVITY", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, provenance) VALUES ($SE,'EmailReceived','email','m1',now(),$ACCOUNT,$OPP,'{"source_system":"email","source_object_id":"m1"}') RETURNING id`},
		{"TRIGGER", `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ($ACCOUNT,'post_interaction_followup',true,'{eligible_customer_replied}') RETURNING id`},
		{"TRIGGER2", `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ($ACCOUNT,'post_interaction_followup',true,'{eligible_stakeholder_change}') RETURNING id`},
		{"INELIGIBLE", `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ($ACCOUNT,'post_interaction_followup',false,'{no_material_change}') RETURNING id`},
		{"RUN", `INSERT INTO agent_runs (account_id, opportunity_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids) VALUES ($ACCOUNT,$OPP,'post_interaction_followup','dry_run','awaiting_human',$TRIGGER,ARRAY[$ACTIVITY]::uuid[]) RETURNING id`},
	}
	for _, st := range steps {
		var id string
		if err := tx.QueryRow(ids.expand(st.sql)).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", st.key, err)
		}
		ids[st.key] = id
	}
	return ids
}

// execExpectingError runs q inside a savepoint so the outer test transaction survives.
// Deferred constraints are forced to check before the savepoint is released.
func execExpectingError(tx *sql.Tx, q string) error {
	if _, err := tx.Exec(`SAVEPOINT expect_err`); err != nil {
		return fmt.Errorf("savepoint: %w", err)
	}
	_, execErr := tx.Exec(q)
	if execErr == nil {
		_, execErr = tx.Exec(`SET CONSTRAINTS ALL IMMEDIATE`)
	}
	if _, err := tx.Exec(`ROLLBACK TO SAVEPOINT expect_err`); err != nil {
		return fmt.Errorf("rollback to savepoint: %w", err)
	}
	return execErr
}

func assertSQLState(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected SQLSTATE %s, statement succeeded", want)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected Postgres error %s, got %v", want, err)
	}
	if pgErr.Code != want {
		t.Fatalf("SQLSTATE = %s (%s), want %s", pgErr.Code, pgErr.Message, want)
	}
}

func assertVersion(t *testing.T, ctx context.Context, want int64) {
	t.Helper()
	got, err := env.Migrator.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if got != want {
		t.Fatalf("schema version = %d, want %d", got, want)
	}
}
