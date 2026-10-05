package store_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// Fact JSON fragments shaped like contracts/schemas/state_transition.v1.json#/$defs.
const (
	factEvidence = `"evidence_refs":[{"activity_id":"0ac70000-0000-4000-8000-000000000101"}]`
	supportFact  = `[{"key":"expansion_need_stated","description":"d","required":true,"satisfied":true,` + factEvidence + `}]`
	missingReq   = `[{"key":"owner_stabilized","description":"d","required":true,"satisfied":false,"evidence_refs":[]}]`
	missingOpt   = `[{"key":"economic_buyer_known","description":"d","required":false,"satisfied":false,"evidence_refs":[]}]`
	decisive     = `[{"key":"support_risk_high","description":"d","required":false,"satisfied":true,"rejects":true,` + factEvidence + `}]`
	recorded     = `[{"key":"customer_silent","description":"d","required":false,"satisfied":true,"rejects":false,` + factEvidence + `}]`
)

// transitionInsert builds an INSERT of one transition; the seeded state_history row is version 7.
// extra is "col=value" appended to the column and value lists.
func transitionInsert(id, from, to, status, supporting, missing, contradicting, extra string) string {
	cols := `id, account_id, from_state, to_state_candidate, status, trigger_activity_ids, supporting_facts, missing_facts,
		contradicting_facts, confidence, state_version, rule_set_version, first_observed_at, last_updated_at`
	vals := `'` + id + `',$ACCOUNT,'` + from + `',` + to + `,'` + status + `',ARRAY[$ACTIVITY]::uuid[],'` + supporting + `','` +
		missing + `','` + contradicting + `',0.4,7,'transition_rules:v1',now(),now()`
	for _, kv := range strings.Split(extra, ";") {
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		cols += ", " + parts[0]
		vals += ", " + parts[1]
	}
	return `INSERT INTO state_transitions (` + cols + `) VALUES (` + vals + `)`
}

const (
	tid1 = "1e5a0000-0000-4000-8000-000000000001"
	tid2 = "1e5a0000-0000-4000-8000-000000000002"
	tid3 = "1e5a0000-0000-4000-8000-000000000003"
)

func candidate(id string) string {
	return transitionInsert(id, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, missingReq, "[]", "")
}

func unresolved(id string) string {
	return transitionInsert(id, "REORG", "NULL", "UNRESOLVED", "[]", "[]", "[]", "")
}

func set(id, assignments string) string {
	return `UPDATE state_transitions SET ` + assignments + ` WHERE id='` + id + `'`
}

var transitionCases = []constraintCase{
	{"unknown transition status is rejected",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "PROBABLE", supportFact, missingReq, "[]", ""), checkViolation},
	{"relationship states are upper case",
		transitionInsert(tid2, "reorg", "'EXPANSION'", "CANDIDATE", supportFact, missingReq, "[]", ""), checkViolation},
	{"unknown is never a target",
		transitionInsert(tid2, "REORG", "'unknown'", "CANDIDATE", supportFact, missingReq, "[]", ""), checkViolation},
	{"a transition changes state",
		transitionInsert(tid2, "REORG", "'REORG'", "CANDIDATE", supportFact, missingReq, "[]", ""), checkViolation},
	{"only an UNRESOLVED transition may lack a target",
		transitionInsert(tid2, "REORG", "NULL", "CANDIDATE", supportFact, missingReq, "[]", ""), checkViolation},
	{"an UNRESOLVED transition has no target",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "UNRESOLVED", "[]", "[]", "[]", ""), checkViolation},
	{"a transition is triggered by activity",
		strings.Replace(candidate(tid2), "ARRAY[$ACTIVITY]::uuid[]", "'{}'", 1), checkViolation},
	{"rule set is versioned", strings.Replace(candidate(tid2), "'transition_rules:v1'", "'v1'", 1), checkViolation},
	{"a candidate misses at least one required fact",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, missingOpt, "[]", ""), checkViolation},
	{"a candidate has supporting evidence",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", "[]", missingReq, "[]", ""), checkViolation},
	{"a confirmed transition has no unmet required fact",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CONFIRMED", supportFact, missingReq, "[]", "confirmed_at=now()"), checkViolation},
	{"a confirmed transition has confirmed_at",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CONFIRMED", supportFact, missingOpt, "[]", ""), checkViolation},
	{"only a confirmed transition has confirmed_at",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, missingReq, "[]", "confirmed_at=now()"), checkViolation},
	{"a rejected transition needs a decisive contradiction",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "REJECTED", supportFact, missingReq, recorded, "rejected_at=now()"), checkViolation},
	{"a rejected transition has rejected_at",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "REJECTED", supportFact, missingReq, decisive, ""), checkViolation},
	{"a decisively contradicted candidate must be rejected",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, missingReq, decisive, ""), checkViolation},
	{"a contradiction is never a requirement",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, missingReq,
			strings.Replace(recorded, `"required":false`, `"required":true`, 1), ""), checkViolation},
	{"a contradiction says whether it is decisive",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, missingReq,
			strings.Replace(recorded, `"rejects":false,`, ``, 1), ""), checkViolation},
	{"a satisfied fact rests on evidence",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", strings.Replace(supportFact, factEvidence, `"evidence_refs":[]`, 1),
			missingReq, "[]", ""), checkViolation},
	{"a supporting fact is satisfied",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", strings.Replace(supportFact, `"satisfied":true`, `"satisfied":false`, 1),
			missingReq, "[]", ""), checkViolation},
	{"a missing fact is unsatisfied",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, strings.Replace(missingReq, `"satisfied":false`, `"satisfied":true`, 1),
			"[]", ""), checkViolation},
	{"updates never precede the first observation",
		strings.Replace(candidate(tid2), "now(),now()", "now(),now() - interval '1 day'", 1), checkViolation},
	{"only an UNRESOLVED transition is closed",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CANDIDATE", supportFact, missingReq, "[]", "closed_at=now();close_reason='stale'"), checkViolation},
	{"a closed transition says why",
		transitionInsert(tid2, "REORG", "NULL", "UNRESOLVED", "[]", "[]", "[]", "closed_at=now()"), checkViolation},
	{"the evaluated state version exists",
		strings.Replace(candidate(tid2), ",0.4,7,", ",0.4,99,", 1), foreignKeyViolation},
	{"at most one open transition per account", candidate(tid1) + "; " + unresolved(tid2), uniqueViolation},
	{"confirmed transitions are terminal",
		transitionInsert(tid2, "REORG", "'EXPANSION'", "CONFIRMED", supportFact, missingOpt, "[]", "confirmed_at=now()") +
			"; " + set(tid2, "last_updated_at = now()"), checkViolation},
	{"rejected transitions are terminal",
		candidate(tid1) + "; " + set(tid1, "status='REJECTED', rejected_at=now(), contradicting_facts='"+decisive+"'") +
			"; " + set(tid1, "status='CANDIDATE', rejected_at=NULL, contradicting_facts='[]'"), checkViolation},
	{"closed transitions are terminal",
		unresolved(tid1) + "; " + set(tid1, "closed_at=now(), close_reason='stale'") + "; " + set(tid1, "closed_at=NULL, close_reason=NULL"),
		checkViolation},
	{"a candidate is not re-targeted directly",
		candidate(tid1) + "; " + set(tid1, "to_state_candidate='RECOVERY'"), checkViolation},
	{"a candidate that loses its gate drops its target",
		candidate(tid1) + "; " + set(tid1, "status='UNRESOLVED'"), checkViolation},
	{"a transition id never changes",
		candidate(tid1) + "; " + set(tid1, "id='"+tid3+"'"), checkViolation},
	{"an UNRESOLVED transition is never rejected",
		unresolved(tid1) + "; " + set(tid1, "status='REJECTED', to_state_candidate='EXPANSION', rejected_at=now(), contradicting_facts='"+decisive+"'"),
		checkViolation},
	{"transition history rows are never updated",
		candidate(tid1) + "; UPDATE state_transition_history SET status='CONFIRMED'", checkViolation},
	{"transition history rows are never updated, even by a purge",
		candidate(tid1) + "; SET LOCAL ghost.purge_transitions = 'on'; UPDATE state_transition_history SET status='CONFIRMED'", checkViolation},
	{"transitions cannot be deleted", candidate(tid1) + "; DELETE FROM state_transitions", checkViolation},
	{"transition history rows cannot be deleted", candidate(tid1) + "; DELETE FROM state_transition_history", checkViolation},
	{"transition history cannot be truncated", candidate(tid1) + "; SET CONSTRAINTS ALL IMMEDIATE; TRUNCATE state_transition_history", checkViolation},
	{"transitions cannot be truncated", candidate(tid1) + "; SET CONSTRAINTS ALL IMMEDIATE; TRUNCATE state_transitions CASCADE", checkViolation},
}

// seedTransitionGraph adds the state_history row (version 7) transitions are evaluated against.
func seedTransitionGraph(t *testing.T, tx *sql.Tx) seededIDs {
	t.Helper()
	ids := seedGraph(t, tx)
	if _, err := tx.Exec(ids.expand(`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($ACCOUNT,7,now(),'{}')`)); err != nil {
		t.Fatalf("seed state_history: %v", err)
	}
	return ids
}

func TestStateTransitionConstraints(t *testing.T) {
	for _, tc := range transitionCases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedTransitionGraph(t, tx)
				err := execExpectingError(tx, ids.expand(tc.sql))
				assertSQLState(t, err, tc.wantCode)
			})
		})
	}
}

// runSteps executes statements that must all succeed.
func runSteps(t *testing.T, tx *sql.Tx, ids seededIDs, steps []string) {
	t.Helper()
	for _, s := range append(steps, `SET CONSTRAINTS ALL IMMEDIATE`) {
		if _, err := tx.Exec(ids.expand(s)); err != nil {
			t.Fatalf("step failed: %v\n%s", err, s)
		}
	}
}

func historyOf(t *testing.T, tx *sql.Tx, id string) string {
	t.Helper()
	var got sql.NullString
	if err := tx.QueryRow(`SELECT string_agg(status, ',' ORDER BY id) FROM state_transition_history WHERE transition_id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("history of %s: %v", id, err)
	}
	return got.String
}

// HAR-126: a transition's life is kept in history; contradicting evidence is preserved and a
// REJECTED transition stays inspectable; a stale UNRESOLVED closes and frees the open slot.
func TestStateTransitionLifecycleIsRecorded(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedTransitionGraph(t, tx)
		runSteps(t, tx, ids, []string{
			unresolved(tid1),
			set(tid1, "status='CANDIDATE', to_state_candidate='EXPANSION', supporting_facts='"+supportFact+"', missing_facts='"+missingReq+"', contradicting_facts='"+recorded+"'"),
			set(tid1, "status='UNRESOLVED', to_state_candidate=NULL"),
			set(tid1, "status='CANDIDATE', to_state_candidate='EXPANSION'"),
			set(tid1, "status='CONFIRMED', confirmed_at=now(), confidence=1, missing_facts='"+missingOpt+"'"),
			transitionInsert(tid2, "EXPANSION", "'REORG'", "CANDIDATE", supportFact, missingReq, recorded, ""),
			set(tid2, "status='REJECTED', rejected_at=now(), contradicting_facts='"+strings.TrimSuffix(decisive, "]")+","+strings.TrimPrefix(recorded, "[")+"'"),
			unresolved(tid3),
			set(tid3, "closed_at=now(), close_reason='stale'"),
			transitionInsert("1e5a0000-0000-4000-8000-000000000004", "EXPANSION", "NULL", "UNRESOLVED", "[]", "[]", "[]", ""),
		})
		for id, want := range map[string]string{
			tid1: "UNRESOLVED,CANDIDATE,UNRESOLVED,CANDIDATE,CONFIRMED",
			tid2: "CANDIDATE,REJECTED",
			tid3: "UNRESOLVED,UNRESOLVED",
		} {
			if got := historyOf(t, tx, id); got != want {
				t.Errorf("history of %s = %q, want %q", id, got, want)
			}
		}
		var n int
		err := tx.QueryRow(`SELECT jsonb_array_length(snapshot->'contradicting_facts') FROM state_transition_history
			WHERE transition_id = $1 AND status = 'REJECTED'`, tid2).Scan(&n)
		if err != nil || n != 2 {
			t.Errorf("rejected snapshot keeps the decisive and the recorded contradiction: n=%d err=%v", n, err)
		}
	})
}

// An explicit purge (retention job, test reset) may remove transitions and their history.
func TestStateTransitionsYieldOnlyToAnExplicitPurge(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedTransitionGraph(t, tx)
		runSteps(t, tx, ids, []string{
			candidate(tid1),
			`SET CONSTRAINTS ALL IMMEDIATE`,
			`SET LOCAL ghost.purge_transitions = 'on'`,
			`DELETE FROM state_transition_history`,
			`DELETE FROM state_transitions`,
			`TRUNCATE state_transitions, state_transition_history`,
		})
	})
}

// Migration 0021 adds state_transition_support to the eval_type domain.
func TestStateTransitionSupportIsAKnownEvaluator(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runSteps(t, tx, ids, []string{
			`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,1,'account_agent','{}')`,
			`INSERT INTO eval_runs (agent_run_id, evaluator, evaluator_version, kind, verdict, draft_index, label, evidence_class)
			 VALUES ($RUN,'state_transition_support','state_transition_support:v1','deterministic','pass',1,'SUPPORTED','product_rule')`,
		})
	})
}

// Migration 0021 adds the evidence claim field paths the transition rules read (ADR-0012).
func TestEvidenceClaimFieldPathsAreAccepted(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		insert := `INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
			VALUES ($ACCOUNT,'%s','%s','first_party_ai',0.9,$ACTIVITY,'quote',now(),'llm:test@v5')`
		runSteps(t, tx, ids, []string{
			fmt.Sprintf(insert, "org_change", `{"kind":"remit_change","summary":"Priya now runs the Americas sites"}`),
			fmt.Sprintf(insert, "expansion_need", `"Berlin and Rotterdam before the January peak"`),
			fmt.Sprintf(insert, "business_value", `"dispatch exceptions down a quarter"`),
		})
	})
}
