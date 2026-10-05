package store_test

import (
	"database/sql"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// The control plane shows a knowledge mutation's preconditions and exceptions from the knowledge row itself. That is
// the version in force at the time of the mutation only because the lifecycle never rewrites them: the database
// enforces it, and a revision path would have to version them (and the read with them).
func TestKnowledgeConditionsCannotBeRewrittenAfterCreation(t *testing.T) {
	for _, col := range []string{"situation_signature", "applicability_conditions", "exceptions"} {
		t.Run(col, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				var id string
				if err := tx.QueryRow(`INSERT INTO knowledge (title, guidance) VALUES ('Keep champion','g') RETURNING id`).Scan(&id); err != nil {
					t.Fatal(err)
				}
				assertSQLState(t, execExpectingError(tx, `UPDATE knowledge SET `+col+` = '[{"changed":true}]'::jsonb WHERE id = '`+id+`'`), checkViolation)
			})
		})
	}
}

func TestKnowledgeStatusAndTalliesStillUpdate(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		var id string
		if err := tx.QueryRow(`INSERT INTO knowledge (title, guidance) VALUES ('Keep champion','g') RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE knowledge SET status = 'provisional', support_count = 2, exceptions = exceptions WHERE id = $1`, id); err != nil {
			t.Fatalf("a status or tally update was refused: %v", err)
		}
	})
}
