package store_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// signalContract is the part of contracts/schemas/signal.v1.json the schema-parity tests read.
type signalContract struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Defs       struct {
		Event    struct{ Enum []string } `json:"eventSignalType"`
		Standing struct{ Enum []string } `json:"standingSignalType"`
	} `json:"$defs"`
}

func loadSignalContract(t *testing.T) signalContract {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "contracts", "schemas", "signal.v1.json"))
		if err == nil {
			var c signalContract
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			return c
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("contracts/schemas/signal.v1.json not found")
		}
		dir = filepath.Dir(dir)
	}
}

// Every signal.v1.json property is a signals column of the same name (JSON <-> SQL parity), including
// subject_claim_id, which had no column before migration 0016.
func TestEverySignalPropertyHasAColumn(t *testing.T) {
	c := loadSignalContract(t)
	if len(c.Properties) < 12 {
		t.Fatalf("signal contract lost properties: %d", len(c.Properties))
	}
	for name := range c.Properties {
		var n int
		err := env.DB.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'signals' AND column_name = $1`, name).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("signals.%s missing (n=%d, err=%v)", name, n, err)
		}
	}
}

const insertSignal = `INSERT INTO signals (account_id, signal_type, rule, occurred_at, expires_at, dedupe_key, subject_claim_id)
	VALUES ($ACCOUNT, '%s', 'sig.test@1', '2026-09-29T15:42:00Z', %s, %s, %s)`

func signalSQL(typ, expires, dedupe, claim string) string {
	return fmt.Sprintf(insertSignal, typ, expires, dedupe, claim)
}

// EVENT signals must carry a window end at or after occurred_at; STANDING signals must not (signal.v1.json allOf).
func TestSignalWindowMatchesKind(t *testing.T) {
	c := loadSignalContract(t)
	if len(c.Defs.Event.Enum) != 12 || len(c.Defs.Standing.Enum) != 7 {
		t.Fatalf("signal kinds changed: %d event, %d standing", len(c.Defs.Event.Enum), len(c.Defs.Standing.Enum))
	}
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		for _, typ := range c.Defs.Event.Enum {
			assertSQLState(t, execExpectingError(tx, ids.expand(signalSQL(typ, "'2026-09-01T00:00:00Z'", "NULL", "NULL"))), checkViolation)
			// a writer that omits the window gets the default 14 days
			if _, err := tx.Exec(ids.expand(signalSQL(typ, "NULL", "NULL", "NULL"))); err != nil {
				t.Errorf("event signal %s without a window must get the default: %v", typ, err)
			}
			var days float64
			if err := tx.QueryRow(`SELECT extract(epoch FROM (expires_at - occurred_at)) / 86400 FROM signals ORDER BY created_at DESC, id LIMIT 1`).Scan(&days); err != nil || days != 14 {
				t.Errorf("default window of %s = %v days (%v)", typ, days, err)
			}
			if _, err := tx.Exec(ids.expand(signalSQL(typ, "'2026-10-13T15:42:00Z'", "NULL", "NULL"))); err != nil {
				t.Errorf("event signal %s with a window: %v", typ, err)
			}
		}
		for _, typ := range c.Defs.Standing.Enum {
			assertSQLState(t, execExpectingError(tx, ids.expand(signalSQL(typ, "'2026-10-13T15:42:00Z'", "NULL", "NULL"))), checkViolation)
			if _, err := tx.Exec(ids.expand(signalSQL(typ, "NULL", "NULL", "NULL"))); err != nil {
				t.Errorf("standing signal %s without a window: %v", typ, err)
			}
		}
	})
}

func TestSignalDedupeKeyIsUniquePerAccount(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		first := signalSQL("customer_replied", "'2026-10-13T15:42:00Z'", "'k1'", "NULL")
		if _, err := tx.Exec(ids.expand(first)); err != nil {
			t.Fatal(err)
		}
		assertSQLState(t, execExpectingError(tx, ids.expand(first)), uniqueViolation)
		if _, err := tx.Exec(ids.expand(signalSQL("customer_replied", "'2026-10-13T15:42:00Z'", "NULL", "NULL"))); err != nil {
			t.Errorf("a signal without a key is not constrained: %v", err)
		}
		other := `INSERT INTO signals (account_id, signal_type, rule, expires_at, dedupe_key) VALUES ($OTHER_ACCOUNT, 'customer_replied', 'sig.test@1', now(), 'k1')`
		if _, err := tx.Exec(ids.expand(other)); err != nil {
			t.Errorf("the key is unique per account, not globally: %v", err)
		}
	})
}

func TestSignalSubjectClaimMustExist(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		missing := signalSQL("security_blocker_appeared", "NULL", "NULL", "'11111111-1111-4111-8111-111111111111'")
		assertSQLState(t, execExpectingError(tx, ids.expand(missing)), foreignKeyViolation)
	})
}

func TestOneTriggerEvaluationPerDiffAndWorkflow(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		for _, q := range []string{
			`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($ACCOUNT, 1, now(), '{}')`,
			`INSERT INTO state_diffs (id, account_id, from_version, to_version, is_material, changes, activity_ids)
			 VALUES ('22222222-2222-4222-8222-222222222222', $ACCOUNT, 0, 1, false, '[]', ARRAY[$ACTIVITY]::uuid[])`,
			`INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, state_diff_id)
			 VALUES ($ACCOUNT, 'post_interaction_followup', false, '{no_material_change}', '22222222-2222-4222-8222-222222222222')`,
		} {
			if _, err := tx.Exec(ids.expand(q)); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		again := `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, state_diff_id)
			 VALUES ($ACCOUNT, 'post_interaction_followup', false, '{no_relevant_signal}', '22222222-2222-4222-8222-222222222222')`
		assertSQLState(t, execExpectingError(tx, ids.expand(again)), uniqueViolation)
	})
}
