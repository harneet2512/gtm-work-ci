package store_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// Migration 0026 (Episode Replay bookkeeping): one demo_episodes row per released position and the
// demo_replays cursor. Reuses the seeded world and the play manifest of replay_play_test.go.

const episodeSQL = `INSERT INTO demo_episodes (manifest_id, position, account_id, event_id, source_event_id, activity_id, material, no_action_reason)
 VALUES ('a1a10000-0000-4000-8000-000000000001', 1, $ACCOUNT, 'e7e00000-0000-4000-8000-000000000001', $SE, $ACTIVITY, true, NULL)`

// episodeWith is an episode insert whose material flag and no-action reason are the arguments.
func episodeWith(material, reason string) string {
	return `INSERT INTO demo_episodes (manifest_id, position, account_id, event_id, source_event_id, activity_id, material, no_action_reason)
 VALUES ('a1a10000-0000-4000-8000-000000000001', 1, $ACCOUNT, 'e7e00000-0000-4000-8000-000000000001', $SE, $ACTIVITY, ` + material + `, ` + reason + `)`
}

func TestDemoEpisodeRules(t *testing.T) {
	cases := []struct{ name, sql, want string }{
		{"a released position is recorded once",
			episodeSQL + `; ` + episodeSQL, uniqueViolation},
		{"a manifest releases an event once",
			episodeSQL + `; ` + strings.Replace(episodeSQL, `, 1, $ACCOUNT,`, `, 2, $ACCOUNT,`, 1), uniqueViolation},
		{"positions are 1-based",
			strings.Replace(episodeSQL, `VALUES ('a1a10000-0000-4000-8000-000000000001', 1`, `VALUES ('a1a10000-0000-4000-8000-000000000001', 0`, 1), checkViolation},
		{"a material episode carries no no-action reason",
			episodeWith("true", `'no_material_change'`), checkViolation},
		{"a non-material episode must say why no action was needed",
			episodeWith("false", "NULL"), checkViolation},
		{"an episode needs its manifest",
			strings.Replace(episodeSQL, "a1a10000-0000-4000-8000-000000000001", "a1a10000-0000-4000-8000-0000000000ff", 1), foreignKeyViolation},
		{"an episode needs its source event",
			strings.Replace(episodeSQL, "$SE", "'e7e00000-0000-4000-8000-0000000000ff'", 1), foreignKeyViolation},
		{"an episode needs its activity",
			strings.Replace(episodeSQL, "$ACTIVITY", "'a0000000-0000-4000-8000-0000000000ff'", 1), foreignKeyViolation},
		{"an empty no-action reason is not a reason",
			episodeWith("false", `''`), checkViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
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

func TestAnEpisodeLinksTheRowsItsEventWrote(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{playManifestSQL,
			`WITH j AS (INSERT INTO graph_projection_jobs (account_id, claimed_at, claimed_by, lease_expires_at, completed_at)
			            VALUES ($ACCOUNT, now(), 'test', now(), now()) RETURNING id),
			      g AS (INSERT INTO graph_projection_diffs (job_id, account_id, source_event_ids, summary)
			            SELECT id, $ACCOUNT, ARRAY[$SE]::uuid[], '{}'::jsonb FROM j RETURNING id)
			 INSERT INTO demo_episodes (manifest_id, position, account_id, event_id, source_event_id, activity_id, state_diff_id,
			   state_version, material, account_change_id, decision_episode_id, graph_diff_id)
			 SELECT 'a1a10000-0000-4000-8000-000000000001', 2, $ACCOUNT, '` + event129 + `', $SE, $ACTIVITY, '` + diff129 + `',
			   2, true, '` + change129 + `', '` + epi129 + `', id FROM g`})
	})
}

func TestAnEpisodesStateVersionMustBeAStoredVersion(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{playManifestSQL})
		assertSQLState(t, execExpectingError(tx, ids.expand(
			`INSERT INTO demo_episodes (manifest_id, position, account_id, event_id, source_event_id, activity_id, state_version, material)
			 VALUES ('a1a10000-0000-4000-8000-000000000001', 1, $ACCOUNT, 'e7e00000-0000-4000-8000-000000000001', $SE, $ACTIVITY, 9, true)`)),
			foreignKeyViolation)
	})
}

func TestAnEpisodesGraphDiffNamesARealProjectionDiff(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{playManifestSQL})
		assertSQLState(t, execExpectingError(tx, ids.expand(
			`INSERT INTO demo_episodes (manifest_id, position, account_id, event_id, source_event_id, activity_id, material, graph_diff_id)
			 VALUES ('a1a10000-0000-4000-8000-000000000001', 1, $ACCOUNT, 'e7e00000-0000-4000-8000-000000000001', $SE, $ACTIVITY, true, 424242)`)),
			foreignKeyViolation)
	})
}

func TestDemoReplayCursorRules(t *testing.T) {
	cases := []struct{ name, sql, want string }{
		{"the cursor never goes negative",
			`INSERT INTO demo_replays (manifest_id, released) VALUES ('a1a10000-0000-4000-8000-000000000001', -1)`, checkViolation},
		{"a manifest has one cursor",
			`INSERT INTO demo_replays (manifest_id) VALUES ('a1a10000-0000-4000-8000-000000000001')` +
				`; INSERT INTO demo_replays (manifest_id) VALUES ('a1a10000-0000-4000-8000-000000000001')`, uniqueViolation},
		{"the cursor needs its manifest",
			`INSERT INTO demo_replays (manifest_id) VALUES ('a1a10000-0000-4000-8000-0000000000ff')`, foreignKeyViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
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
