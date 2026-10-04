package readmodel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

var wt0 = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

// ADR-0019: before_id is the id of the cursor row, which occurred exactly at `before`. Anything else would
// widen the strict `before`.
func TestTimelineKeysetCursorMustBeTheRowAtBefore(t *testing.T) {
	account, ids := newAccountWithActivities(t, "keyset", []time.Time{wt0, wt0.Add(time.Hour), wt0.Add(2 * time.Hour)})
	other, otherIDs := newAccountWithActivities(t, "keyset-other", []time.Time{wt0.Add(time.Hour)})
	ctx := context.Background()
	at := wt0.Add(time.Hour)

	page, err := reader(t).TimelinePage(ctx, account, 10, &at, ids[1])
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[0] {
		t.Fatalf("the cursor row at its own instant pages on: %+v %v", page.Items, err)
	}
	for name, id := range map[string]string{
		"an unknown id":            "99999999-9999-4999-8999-999999999999",
		"a row at another instant": ids[0],
		"a later row":              ids[2],
		"a row of another account": otherIDs[0],
	} {
		if _, err := reader(t).TimelinePage(ctx, account, 10, &at, id); !errors.Is(err, readmodel.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	_ = other
}

func TestStateWorldAsOfReadsTheStoredVersionBeforeT(t *testing.T) {
	account := scalar(t, `INSERT INTO accounts (name) VALUES ('stateworld') RETURNING id::text`)
	ctx := context.Background()
	if _, err := reader(t).StateWorldAsOf(ctx, account, wt0); !errors.Is(err, readmodel.ErrNoStateBefore) {
		t.Fatalf("no version yet: %v", err)
	}
	exec(t, `INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, 1, $2, '{"version":1}')`, account, wt0)
	if raw, err := reader(t).StateWorldAsOf(ctx, account, wt0.Add(time.Microsecond)); err != nil || string(raw) != `{"version": 1}` {
		t.Fatalf("after the version: %s %v", raw, err)
	}
	if _, err := reader(t).StateWorldAsOf(ctx, account, wt0); !errors.Is(err, readmodel.ErrNoStateBefore) {
		t.Fatalf("strictly before: %v", err)
	}
	if _, err := reader(t).StateWorldAsOf(ctx, "not-a-uuid", wt0); !errors.Is(err, readmodel.ErrNotFound) {
		t.Fatalf("bad account: %v", err)
	}
}

// A run pinned to a state version must not see the diffs of a later version, whatever its as_of.
func TestQueryDiffsBeforeStopsAtTheMaxVersion(t *testing.T) {
	account := scalar(t, `INSERT INTO accounts (name) VALUES ('diffsmax') RETURNING id::text`)
	for _, v := range []int{1, 2, 3} {
		exec(t, `INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, $2, $3, '{}')`, account, v, wt0)
		exec(t, `INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes) VALUES ($1::uuid, $2, $3, true, '[{"field":"stage","op":"changed","material":true}]'::jsonb)`, account, v-1, v)
	}
	cutoff := wt0.Add(time.Second)
	got, err := readmodel.QueryDiffsBefore(context.Background(), env.DB, account, 10, true, "", &cutoff, 2)
	if err != nil || len(got) != 2 || got[0].ToVersion != 2 {
		t.Fatalf("diffs up to version 2 = %+v %v", got, err)
	}
	if got, err = readmodel.QueryDiffsBefore(context.Background(), env.DB, account, 10, true, "", &cutoff, 0); err != nil || len(got) != 3 {
		t.Fatalf("no version bound = %d diffs, %v", len(got), err)
	}
}
