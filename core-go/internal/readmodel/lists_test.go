package readmodel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// pageAll follows next_cursor to the end, failing on a cursor loop.
func pageAllAccounts(t *testing.T, limit int) []readmodel.Account {
	t.Helper()
	var all []readmodel.Account
	cursor := ""
	for i := 0; i < 1000; i++ {
		page, err := reader(t).ListAccounts(context.Background(), limit, cursor)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil {
			return all
		}
		cursor = *page.NextCursor
	}
	t.Fatal("account cursor never ended")
	return nil
}

func TestListAccountsPagesByNameWithoutGapsOrRepeats(t *testing.T) {
	for _, n := range []string{"Zeta List Acct", "Alpha List Acct", "Mid List Acct"} {
		seedAccount(t, n)
	}
	whole := pageAllAccounts(t, readmodel.MaxLimit)
	paged := pageAllAccounts(t, 2)
	if len(paged) != len(whole) || len(whole) < 3 {
		t.Fatalf("paging by 2 saw %d accounts, one big page %d", len(paged), len(whole))
	}
	seen := map[string]bool{}
	for i, a := range paged {
		if seen[a.ID] {
			t.Fatalf("account %s listed twice", a.ID)
		}
		seen[a.ID] = true
		if a.ID != whole[i].ID {
			t.Fatalf("page order diverges at %d: %s vs %s", i, a.ID, whole[i].ID)
		}
		if i > 0 && a.Name < paged[i-1].Name {
			t.Fatalf("not ordered by name: %q after %q", a.Name, paged[i-1].Name)
		}
	}
}

func TestListAccountsItemsAreFullSummaries(t *testing.T) {
	id, runID := seedAccount(t, "Summary List Acct")
	for _, a := range pageAllAccounts(t, readmodel.MaxLimit) {
		if a.ID != id {
			continue
		}
		if a.Stage == nil || *a.Stage != "negotiation" || a.OpenRunID == nil || *a.OpenRunID != runID {
			t.Fatalf("listed summary = %+v", a)
		}
		return
	}
	t.Fatal("the seeded account is not listed")
}

func TestListAccountsRefusesBadArguments(t *testing.T) {
	r := reader(t)
	for name, tc := range map[string]struct {
		limit  int
		cursor string
	}{
		"limit too large":   {readmodel.MaxLimit + 1, ""},
		"negative limit":    {-1, ""},
		"garbage cursor":    {10, "not*base64*"},
		"foreign cursor":    {10, "Zm9yZWlnbg"}, // base64url("foreign"): decodes, but is not an account cursor
		"cursor with a bad": {10, "AAEC"},
	} {
		if _, err := r.ListAccounts(context.Background(), tc.limit, tc.cursor); !errors.Is(err, readmodel.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestListRunsIsNewestFirstAndFiltersByAccountAndStatus(t *testing.T) {
	acct, run := seedAccount(t, "Run List Acct")
	other, otherRun := seedAccount(t, "Other Run List Acct")

	page, err := reader(t).ListRuns(context.Background(), readmodel.RunFilter{AccountID: acct})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != run || page.NextCursor != nil {
		t.Fatalf("account filter = %+v", page)
	}
	if page.Items[0].Generation == nil || page.Items[0].Generation.Phase == "" {
		t.Fatalf("a listed run carries its generation status: %+v", page.Items[0].Generation)
	}

	none, err := reader(t).ListRuns(context.Background(), readmodel.RunFilter{AccountID: acct, Status: "executed"})
	if err != nil || len(none.Items) != 0 || none.NextCursor != nil {
		t.Fatalf("status filter = %+v %v", none, err)
	}

	all, err := reader(t).ListRuns(context.Background(), readmodel.RunFilter{Status: "awaiting_human", Limit: readmodel.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for i, r := range all.Items {
		got[r.ID] = true
		if r.Status != "awaiting_human" {
			t.Fatalf("status filter leaked %s", r.Status)
		}
		if i > 0 && r.CreatedAt.After(all.Items[i-1].CreatedAt) {
			t.Fatalf("not newest first at %d", i)
		}
	}
	if !got[run] || !got[otherRun] || other == "" {
		t.Fatalf("both seeded runs are listed: %v", got)
	}
}

func TestListRunsPagesWithoutGapsOrRepeats(t *testing.T) {
	acct, _ := seedAccount(t, "Run Page Acct")
	// More runs for the one account: a terminal status frees the open-run slot.
	for i := 0; i < 4; i++ {
		exec(t, `UPDATE agent_runs SET status = 'cancelled' WHERE account_id = $1::uuid AND status = 'awaiting_human'`, acct)
		evalID := scalar(t, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, explanation)
VALUES ($1::uuid, 'post_interaction_followup', true, ARRAY['eligible_customer_replied'], 'paging') RETURNING id::text`, acct)
		act := scalar(t, `SELECT id::text FROM activities WHERE account_id = $1::uuid LIMIT 1`, acct)
		exec(t, `INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
VALUES ($1::uuid, 'post_interaction_followup', 'dry_run', 'awaiting_human', $2::uuid, ARRAY[$3::uuid])`, acct, evalID, act)
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		page, err := reader(t).ListRuns(context.Background(), readmodel.RunFilter{AccountID: acct, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, r := range page.Items {
			if seen[r.ID] {
				t.Fatalf("run %s listed twice", r.ID)
			}
			seen[r.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("saw %d runs in %d pages, want 5 in 3", len(seen), pages)
	}
}

func TestListRunsRefusesBadArguments(t *testing.T) {
	r := reader(t)
	for name, f := range map[string]readmodel.RunFilter{
		"unknown status":         {Status: "sleeping"},
		"malformed account":      {AccountID: "not-a-uuid"},
		"limit out of range":     {Limit: readmodel.MaxLimit + 1},
		"garbage cursor":         {Cursor: "%%%"},
		"cursor of another sort": {Cursor: "Zm9yZWlnbg"},
	} {
		if _, err := r.ListRuns(context.Background(), f); !errors.Is(err, readmodel.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}
