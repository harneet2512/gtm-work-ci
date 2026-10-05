package readmodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

const missing = "99999999-9999-4999-8999-999999999999"

func reader(t *testing.T) *readmodel.Reader {
	t.Helper()
	r, err := readmodel.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}

func exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := env.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestNewRequiresADatabase(t *testing.T) {
	if _, err := readmodel.New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestStateIsTheStoredProjectionAndAsOfReadsHistory(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	ctx := context.Background()
	raw, err := reader(t).State(ctx, w.AccountA, nil)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		AccountID string `json:"account_id"`
		Version   int    `json:"version"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.AccountID != w.AccountA || st.Version < 1 {
		t.Fatalf("state = %+v %v", st, err)
	}

	later := time.Now().Add(time.Hour)
	asOf, err := reader(t).State(ctx, w.AccountA, &later)
	if err != nil {
		t.Fatalf("as_of in the future: %v", err)
	}
	var hist struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(asOf, &hist)
	if hist.Version != st.Version {
		t.Fatalf("as_of(now+1h) version %d, current %d", hist.Version, st.Version)
	}

	early := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := reader(t).State(ctx, w.AccountA, &early); !errors.Is(err, readmodel.ErrNoState) {
		t.Fatalf("as_of before the first state: %v", err)
	}
}

func TestStateErrors(t *testing.T) {
	ctxfixture.Get(t, env.DB)
	ctx := context.Background()
	if _, err := reader(t).State(ctx, missing, nil); !errors.Is(err, readmodel.ErrNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := reader(t).State(ctx, "not-a-uuid", nil); !errors.Is(err, readmodel.ErrNotFound) {
		t.Fatalf("malformed id: %v", err)
	}
	account := scalar(t, `INSERT INTO accounts (name) VALUES ('Stateless') RETURNING id::text`)
	if _, err := reader(t).State(ctx, account, nil); !errors.Is(err, readmodel.ErrNoState) {
		t.Fatalf("account without state: %v", err)
	}
}

func TestDiffsAndSignalsAreEmptyListsUntilHAR106WritesThem(t *testing.T) {
	ctxfixture.Get(t, env.DB)
	account := scalar(t, `INSERT INTO accounts (name) VALUES ('No diffs') RETURNING id::text`)
	ctx := context.Background()
	diffs, err := reader(t).Diffs(ctx, account, 0, true)
	if err != nil || diffs == nil || len(diffs) != 0 {
		t.Fatalf("diffs = %v %v", diffs, err)
	}
	signals, err := reader(t).Signals(ctx, account, 0)
	if err != nil || signals == nil || len(signals) != 0 {
		t.Fatalf("signals = %v %v", signals, err)
	}
	for _, id := range []string{missing, "x"} {
		if _, err := reader(t).Diffs(ctx, id, 0, true); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("diffs of %q: %v", id, err)
		}
		if _, err := reader(t).Signals(ctx, id, 0); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("signals of %q: %v", id, err)
		}
	}
}

func TestDiffsAndSignalsListNewestFirstWithFilters(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	ctx := context.Background()
	insertDiff(t, w.AccountA, 2001, true, `[{"field":"stage","op":"changed","material":true}]`)
	insertDiff(t, w.AccountA, 2002, false, `[{"field":"summary","op":"changed","material":false}]`)
	insertDiff(t, w.AccountA, 2003, true, `[{"field":"champion","op":"changed","material":true}]`)

	material, err := reader(t).Diffs(ctx, w.AccountA, 0, true)
	if err != nil || len(material) != 2 || material[0].ToVersion != 2003 || material[1].ToVersion != 2001 {
		t.Fatalf("material diffs = %+v %v", material, err)
	}
	all, err := reader(t).Diffs(ctx, w.AccountA, 2, false)
	if err != nil || len(all) != 2 || all[0].ToVersion != 2003 || all[1].ToVersion != 2002 {
		t.Fatalf("limit 2 diffs = %+v %v", all, err)
	}
	if _, err := reader(t).Diffs(ctx, w.AccountA, readmodel.MaxLimit+1, true); !errors.Is(err, readmodel.ErrInvalid) {
		t.Fatalf("over-limit: %v", err)
	}

	exec(t, `INSERT INTO signals (account_id, signal_type, rule, details, evidence_refs, created_at) VALUES
 ($1::uuid, 'customer_replied', 'sig.reply@1', '{}', '[]', now() - interval '2 hours'),
 ($1::uuid, 'pricing_interest', 'sig.price@1', '{"k":1}', '[]', now() - interval '1 hour')`, w.AccountA)
	signals, err := reader(t).Signals(ctx, w.AccountA, 0)
	if err != nil || len(signals) != 2 || signals[0].SignalType != "pricing_interest" || signals[1].SignalType != "customer_replied" {
		t.Fatalf("signals = %+v %v", signals, err)
	}
}

func insertDiff(t *testing.T, account string, toVersion int, material bool, changes string) {
	t.Helper()
	exec(t, `INSERT INTO state_history (account_id, version, as_of, state)
 SELECT account_id, $2, as_of, state FROM account_state WHERE account_id = $1::uuid ON CONFLICT DO NOTHING`, account, toVersion)
	exec(t, `INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes, activity_ids)
 VALUES ($1::uuid, 1, $2, $3, $4::jsonb, '{}')`, account, toVersion, material, changes)
}
