package changedim_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/changedim"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var nextVersion = 8000

// storeDiff writes one state diff (the table requires a diff with a material change to say so) with the given changes JSON and returns its id.
func storeDiff(t *testing.T, changes string) string {
	t.Helper()
	w := ctxfixture.Get(t, env.DB)
	nextVersion++
	if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state)
 SELECT account_id, $2::int, as_of, state FROM account_state WHERE account_id = $1::uuid ON CONFLICT DO NOTHING`, w.AccountA, nextVersion); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := env.DB.QueryRow(`INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes)
 VALUES ($1::uuid, 1, $2::int, $3::boolean, $4::jsonb) RETURNING id::text`, w.AccountA, nextVersion, strings.Contains(changes, `"material":true`), changes).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func topics(t *testing.T, changes string) string {
	t.Helper()
	got, err := changedim.DiffTopics(context.Background(), env.DB, storeDiff(t, changes))
	if err != nil {
		t.Fatalf("DiffTopics: %v", err)
	}
	return strings.Join(got, ",")
}

func TestDiffTopicsAreTheDimensionsOfTheMaterialChanges(t *testing.T) {
	got := topics(t, `[
 {"field":"stage","op":"changed","material":true},
 {"field":"objections","op":"added","material":true},
 {"field":"blockers","op":"added","material":true},
 {"field":"current_commitments","op":"changed","material":true}]`)
	// sorted and unique: stage is buyer_intent, objections and blockers are both blockers_risk
	if want := "blockers_risk,buyer_intent,next_step_commitment"; got != want {
		t.Fatalf("topics = %q, want %q", got, want)
	}
}

func TestDiffTopicsLeavesOutAChangeThatIsNotMaterial(t *testing.T) {
	got := topics(t, `[
 {"field":"stage","op":"changed","material":true},
 {"field":"owner","op":"changed","material":false},
 {"field":"next_meeting","op":"changed"}]`)
	if got != "buyer_intent" {
		t.Fatalf("a non-material or unmarked change contributed a topic: %q", got)
	}
}

func TestDiffTopicsOfADiffWithNothingMaterialOrKnownIsNone(t *testing.T) {
	for name, changes := range map[string]string{
		"empty diff":       `[]`,
		"none material":    `[{"field":"stage","material":false}]`,
		"unmapped field":   `[{"field":"not_a_field","material":true}]`,
		"json null column": `null`,
	} {
		got, err := changedim.DiffTopics(context.Background(), env.DB, storeDiff(t, changes))
		if err != nil || got != nil {
			t.Errorf("%s: topics = %v, err = %v; want nil, nil", name, got, err)
		}
	}
}

func TestDiffTopicsOfNoDiffIsNone(t *testing.T) {
	got, err := changedim.DiffTopics(context.Background(), env.DB, "")
	if err != nil || got != nil {
		t.Fatalf("an empty diff id has no topic: %v %v", got, err)
	}
}

func TestDiffTopicsNamesTheDiffWhenItIsMissing(t *testing.T) {
	const missing = "99999999-9999-4999-8999-999999999999"
	if _, err := changedim.DiffTopics(context.Background(), env.DB, missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("a missing diff must be an error naming it, got %v", err)
	}
}
