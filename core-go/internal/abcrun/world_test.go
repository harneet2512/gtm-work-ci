package abcrun_test

import (
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/abcrun"
)

func scalar(t testing.TB, q string, args ...any) string {
	t.Helper()
	var v *string
	if err := env.DB.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if v == nil {
		return ""
	}
	return *v
}

func newBuilder(t testing.TB) *abcrun.Builder {
	t.Helper()
	purge(t)
	if err := abcrun.InstallDeterministicIDs(bg, env.DB); err != nil {
		t.Fatal(err)
	}
	b, err := abcrun.NewBuilder(env.DB, t0.Add(100*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SeedCompany(bg, company(), t0.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestABuiltWorldHasTheStageAndTheTruthExtractorsObjection(t *testing.T) {
	b := newBuilder(t)
	ref, err := b.Build(bg, situation("S-1", "discriminating"))
	if err != nil {
		t.Fatal(err)
	}
	state := scalar(t, `SELECT state::text FROM account_state WHERE account_id = $1::uuid`, ref.AccountID)
	for _, want := range []string{`"Quote"`, "Pricing is higher than planned"} {
		if !strings.Contains(state, want) {
			t.Fatalf("account state lacks %q:\n%s", want, state)
		}
	}
	if ref.TriggerID == "" || ref.StateVersion < 1 {
		t.Fatalf("ref = %+v", ref)
	}
}
