package store_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// insertKnowledgeWithChange creates a knowledge row and moves it to provisional, citing a reaction.
func insertKnowledgeWithChange(t *testing.T, tx *sql.Tx) string {
	t.Helper()
	var id string
	if err := tx.QueryRow(`INSERT INTO knowledge (title, guidance) VALUES ('Keep champion','g') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, err := tx.Exec(`SELECT set_config('ghost.knowledge_reason', 'earned provisional', true),
		set_config('ghost.knowledge_evidence_kind', 'customer_reaction', true),
		set_config('ghost.knowledge_evidence_ref', '66666666-6666-4666-8666-666666666666', true)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE knowledge SET status = 'provisional' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestKnowledgeHistoryRecordsTheEvidenceOfAChange(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		id := insertKnowledgeWithChange(t, tx)
		var reason, kind, ref string
		err := tx.QueryRow(`SELECT reason, evidence_kind, evidence_ref_id::text FROM knowledge_status_history
			WHERE knowledge_id = $1 AND to_status = 'provisional'`, id).Scan(&reason, &kind, &ref)
		if err != nil {
			t.Fatal(err)
		}
		if reason != "earned provisional" || kind != "customer_reaction" || ref != "66666666-6666-4666-8666-666666666666" {
			t.Fatalf("history row = %q %q %q", reason, kind, ref)
		}
	})
}

func TestKnowledgeHistoryWithoutEvidenceSettingsStoresNulls(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		var id string
		if err := tx.QueryRow(`INSERT INTO knowledge (title, guidance) VALUES ('t','g') RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		var kind, ref sql.NullString
		err := tx.QueryRow(`SELECT evidence_kind, evidence_ref_id::text FROM knowledge_status_history WHERE knowledge_id = $1`, id).
			Scan(&kind, &ref)
		if err != nil {
			t.Fatal(err)
		}
		if kind.Valid || ref.Valid {
			t.Fatalf("expected null evidence, got %v %v", kind, ref)
		}
	})
}

func TestKnowledgeHistoryIsAppendOnly(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"update", `UPDATE knowledge_status_history SET reason = 'rewritten' WHERE knowledge_id = $K`},
		{"delete", `DELETE FROM knowledge_status_history WHERE knowledge_id = $K`},
		{"truncate", `TRUNCATE knowledge_status_history`},
		{"update even when purging", `SET LOCAL ghost.purge_knowledge = 'on'; UPDATE knowledge_status_history SET reason = 'x' WHERE knowledge_id = $K`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				id := insertKnowledgeWithChange(t, tx)
				err := execExpectingError(tx, strings.ReplaceAll(tc.sql, "$K", "'"+id+"'"))
				assertSQLState(t, err, checkViolation)
			})
		})
	}
}

func TestKnowledgeHistoryPurgeJobMayDelete(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		id := insertKnowledgeWithChange(t, tx)
		if _, err := tx.Exec(`SET LOCAL ghost.purge_knowledge = 'on'`); err != nil {
			t.Fatal(err)
		}
		res, err := tx.Exec(`DELETE FROM knowledge_status_history WHERE knowledge_id = $1`, id)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := res.RowsAffected(); n != 2 {
			t.Fatalf("purged %d rows, want 2", n)
		}
	})
}
