package evaldispute_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestTheTableRefusesWhatTheContractRefuses: the eval_disputes constraints hold even for a writer that
// bypasses the service (JSON <-> SQL parity with eval_dispute.v1.json).
func TestTheTableRefusesWhatTheContractRefuses(t *testing.T) {
	f := newFixture(t)
	failing := f.result("champion_continuity", "fail", true)
	const insert = `INSERT INTO eval_disputes (eval_result_id, agent_run_id, draft_index, eval_type, eval_version,
  disputed_verdict, disputed_blocking, expected_verdict, reason, surface, actor_label)
 VALUES ($1::uuid, $2::uuid, 1, 'champion_continuity', 'champion_continuity:v1', $3, $4, $5, $6, $7, 'Dana')`
	cases := []struct {
		name                       string
		verdict                    string
		blocking                   bool
		expected                   any
		reason, surface, wantState string
	}{
		{"only a fail can have blocked", "warn", true, nil, "x", "web", "23514"},
		{"the expected verdict differs", "fail", true, "fail", "x", "web", "23514"},
		{"an unknown expected verdict", "fail", true, "maybe", "x", "web", "23514"},
		{"not_relevant is never disputed", "not_relevant", false, nil, "x", "web", "23514"},
		{"a blank reason", "fail", true, nil, "   ", "web", "23514"},
		{"an unknown surface", "fail", true, nil, "x", "fax", "23514"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.DB.Exec(insert, failing, f.seed.RunID, tc.verdict, tc.blocking, tc.expected, tc.reason, tc.surface)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.wantState {
				t.Fatalf("want SQLSTATE %s, got %v", tc.wantState, err)
			}
		})
	}
	t.Run("an unknown result", func(t *testing.T) {
		_, err := env.DB.Exec(insert, unknownID, f.seed.RunID, "fail", true, nil, "x", "web")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("want a foreign-key violation, got %v", err)
		}
	})
}
