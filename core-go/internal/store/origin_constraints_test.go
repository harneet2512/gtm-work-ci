package store_test

import (
	"database/sql"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// insertEvent writes one source event with the given origin/provenance SQL literals.
func insertEvent(origin, provenance string) string {
	return `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload, origin, provenance)
VALUES ('email','<syn1-reply-1@acme.example>','received','` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `','{}',` + origin + `,` + provenance + `)`
}

// WP32 (HAR-131): origin/provenance CHECKs mirror source_event.v1.json.
func TestSourceEventOriginConstraints(t *testing.T) {
	rejected := []struct{ name, origin, provenance string }{
		{"unknown origin", `'imagined'`, `NULL`},
		{"synthetic without provenance", `'synthetic'`, `NULL`},
		{"dataset without provenance", `'dataset'`, `NULL`},
		{"provenance without origin", `NULL`, `'synthetic:v1'`},
		{"live with provenance", `'live'`, `'crmarena-pro:b2b'`},
		{"synthetic with a dataset provenance", `'synthetic'`, `'crmarena-pro:b2b'`},
		{"unversioned synthetic layer", `'synthetic'`, `'synthetic:latest'`},
		{"dataset relabelled with a synthetic provenance", `'dataset'`, `'synthetic:v1'`},
		{"malformed provenance", `'dataset'`, `'CRMArena Pro'`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				assertSQLState(t, execExpectingError(tx, insertEvent(tc.origin, tc.provenance)), checkViolation)
			})
		})
	}
	accepted := []struct{ name, origin, provenance string }{
		{"synthetic v1", `'synthetic'`, `'synthetic:v1'`},
		{"dataset replay", `'dataset'`, `'crmarena-pro:b2b'`},
		{"declared live", `'live'`, `NULL`},
		{"undeclared (legacy)", `NULL`, `NULL`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				if _, err := tx.Exec(insertEvent(tc.origin, tc.provenance)); err != nil {
					t.Fatalf("valid origin rejected: %v", err)
				}
			})
		})
	}
}
