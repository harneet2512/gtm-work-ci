package store_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// Migration 0024 (HAR-124): Play's own record, one change per held-out event, the update's transition column.

const playManifestSQL = `INSERT INTO demo_manifests (id, account_id, opportunity_id, data_cutoff, events, held_out_event, why_selected, content_sha256)
 VALUES ('a1a10000-0000-4000-8000-000000000001', $ACCOUNT, $OPP, now(), '[{"event":{"event_id":"e7e00000-0000-4000-8000-000000000001","replay_position":1}}]',
 '{"event_id":"` + event129 + `","replay_position":2,"payload_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}', 'why', '` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `')`

const playSQL = `INSERT INTO demo_plays (manifest_id, held_out_event_id, account_id) VALUES ('a1a10000-0000-4000-8000-000000000001', '` + event129 + `', $ACCOUNT)`

func TestDemoPlayRules(t *testing.T) {
	cases := []struct{ name, sql, want string }{
		{"a play needs its manifest", strings.Replace(playSQL, "a1a10000-0000-4000-8000-000000000001", "a1a10000-0000-4000-8000-0000000000ff", 1), foreignKeyViolation},
		{"a manifest is played once", playSQL + `; ` + playSQL, uniqueViolation},
		{"a play is complete only with its change", playSQL + `; UPDATE demo_plays SET status = 'complete'`, checkViolation},
		{"a play is not complete without a completion time", playSQL + `; UPDATE demo_plays SET status = 'complete', account_change_id = '` + change129 + `'`, checkViolation},
		{"a play is released or complete", playSQL + `; UPDATE demo_plays SET status = 'done'`, checkViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				runWorld(t, tx, ids, strategyWorld(3))
				runWorld(t, tx, ids, []string{playManifestSQL})
				stmts := strings.Split(tc.sql, "; ")
				for _, s := range stmts[:len(stmts)-1] {
					if _, err := tx.Exec(ids.expand(s)); err != nil {
						t.Fatalf("setup: %v\n%s", err, s)
					}
				}
				assertSQLState(t, execExpectingError(tx, ids.expand(stmts[len(stmts)-1])), tc.want)
			})
		})
	}
}

func TestACompletePlayPointsAtItsChange(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{playManifestSQL, playSQL,
			`UPDATE demo_plays SET status = 'complete', account_change_id = '` + change129 + `', completed_at = now()`})
	})
}

func TestAHeldOutEventHasOneAccountChange(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		second := `INSERT INTO account_changes (account_id, opportunity_id, held_out_event_id, trigger_activity_ids, previous_state_ref, current_state_ref, state_diff_id, graph_diff_ref, material_change, evidence_refs)
		 VALUES ($ACCOUNT, $OPP, '` + event129 + `', ARRAY[$ACTIVITY]::uuid[], ` + accountRef(1) + `, ` + accountRef(2) + `, '` + diff129 + `', '{}', true, ` + evidence + `)`
		assertSQLState(t, execExpectingError(tx, ids.expand(second)), uniqueViolation)
	})
	storetest.Tx(t, env.DB, func(tx *sql.Tx) { // live changes (no replay event) are not limited
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		live := `INSERT INTO account_changes (account_id, trigger_activity_ids, previous_state_ref, current_state_ref, state_diff_id, graph_diff_ref, material_change, evidence_refs)
		 VALUES ($ACCOUNT, ARRAY[$ACTIVITY]::uuid[], ` + accountRef(1) + `, ` + accountRef(2) + `, '` + diff129 + `', '{}', true, ` + evidence + `)`
		runWorld(t, tx, ids, []string{live, live})
	})
}

func TestAnUpdatesTransitionIsAnObjectOrNull(t *testing.T) {
	for name, tc := range map[string]struct{ value, want string }{
		"json null is not none":        {`'null'::jsonb`, checkViolation},
		"an array is not a transition": {`'[]'::jsonb`, checkViolation},
	} {
		t.Run(name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				runWorld(t, tx, ids, strategyWorld(3))
				assertSQLState(t, execExpectingError(tx, `UPDATE business_intelligence_updates SET transition = `+tc.value), tc.want)
			})
		})
	}
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{`UPDATE business_intelligence_updates SET transition = '{"status":"CANDIDATE"}'`, `UPDATE business_intelligence_updates SET transition = NULL`})
	})
}

// The manifest pins the payload of event N (migration 0024): every new manifest carries a lowercase sha256.
func TestDemoManifestPinsThePayloadOfEventN(t *testing.T) {
	insert := func(held string) string {
		return `INSERT INTO demo_manifests (account_id, opportunity_id, data_cutoff, events, held_out_event, why_selected, content_sha256)
 VALUES ($ACCOUNT, $OPP, now(), '[{"event":{"event_id":"e7e00000-0000-4000-8000-000000000001","replay_position":1}}]', '` + held + `', 'why', '` +
			strings.Repeat("a", 64) + `')`
	}
	base := `{"event_id":"e7e00000-0000-4000-8000-000000000002","replay_position":2`
	cases := []struct{ name, held, want string }{
		{"no pin", base + `}`, checkViolation},
		{"a pin that is not sha256 hex", base + `,"payload_sha256":"xyz"}`, checkViolation},
		{"an uppercase pin", base + `,"payload_sha256":"` + strings.Repeat("A", 64) + `"}`, checkViolation},
		{"a pin of the wrong length", base + `,"payload_sha256":"` + strings.Repeat("a", 63) + `"}`, checkViolation},
		{"a null pin", base + `,"payload_sha256":null}`, checkViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				assertSQLState(t, execExpectingError(tx, ids.expand(insert(tc.held))), tc.want)
			})
		})
	}
	t.Run("a pinned manifest is accepted", func(t *testing.T) {
		storetest.Tx(t, env.DB, func(tx *sql.Tx) {
			ids := seedGraph(t, tx)
			if _, err := tx.Exec(ids.expand(insert(base + `,"payload_sha256":"` + strings.Repeat("a", 64) + `"}`))); err != nil {
				t.Fatalf("a pinned manifest must be accepted: %v", err)
			}
		})
	})
}
