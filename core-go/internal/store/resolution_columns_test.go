package store_test

import (
	"database/sql"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// Migration 0011 (provenance, re-resolution bookkeeping, back-fill index).
func TestMigration0011ObjectsExist(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		for _, col := range []struct{ table, column string }{
			{"entity_source_mappings", "evidence"}, {"relationships", "evidence"},
			{"unresolved_activities", "attempts"}, {"unresolved_activities", "last_tried_at"},
		} {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`, col.table, col.column).Scan(&n); err != nil || n != 1 {
				t.Errorf("%s.%s missing (n=%d, err=%v)", col.table, col.column, n, err)
			}
		}
		var idx int
		if err := tx.QueryRow(`SELECT count(*) FROM pg_indexes WHERE indexname = 'activity_participants_raw_identity_idx'`).Scan(&idx); err != nil || idx != 1 {
			t.Errorf("activity_participants_raw_identity_idx missing (n=%d, err=%v)", idx, err)
		}
	})
}

// Migration 0015 (WP17): people.internal_only matches the eval input's person.internal_only
// (boolean, never null, false unless set).
func TestPeopleInternalOnlyColumnTypeAndDefault(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		var dataType, nullable, def string
		err := tx.QueryRow(`SELECT data_type, is_nullable, column_default FROM information_schema.columns
			WHERE table_name = 'people' AND column_name = 'internal_only'`).Scan(&dataType, &nullable, &def)
		if err != nil {
			t.Fatalf("people.internal_only: %v", err)
		}
		if dataType != "boolean" || nullable != "NO" || def != "false" {
			t.Fatalf("people.internal_only = %s nullable=%s default=%s; want boolean NOT NULL default false", dataType, nullable, def)
		}
	})
}
