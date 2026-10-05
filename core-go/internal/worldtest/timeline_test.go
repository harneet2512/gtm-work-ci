package worldtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/worldfixture"
)

// insertBulk adds n activities of the account at occurred_at = at(i), the way a large account has them
// (one source event each). It returns their ids.
func insertBulk(t *testing.T, w *worldfixture.World, tag string, n int, atSQL string) []string {
	t.Helper()
	rows, err := env.DB.Query(`
WITH se AS (
  INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
  SELECT 'email', $2::text || i, 'k', md5($2::text || i) || md5(i::text || $2::text), '{}'::jsonb FROM generate_series(1, $3::int) i
  RETURNING id, source_object_id)
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
SELECT se.id, 'EmailReceived', 'email', se.source_object_id, `+atSQL+`, $1::uuid,
       jsonb_build_object('source_system', 'email', 'source_object_id', se.source_object_id)
  FROM se, LATERAL (SELECT substr(se.source_object_id, length($2::text) + 1)::int AS i) n
RETURNING id::text`, w.Account, tag, n)
	if err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	t.Cleanup(func() { // the world is shared: leave it as seeded
		pattern := "^" + tag + "[0-9]+$"
		if _, err := env.DB.Exec(`DELETE FROM activities WHERE account_id = $1::uuid AND source_object_id ~ $2`, w.Account, pattern); err != nil {
			t.Errorf("clean up bulk activities: %v", err)
		}
		if _, err := env.DB.Exec(`DELETE FROM source_events WHERE source_object_id ~ $1`, pattern); err != nil {
			t.Errorf("clean up bulk source events: %v", err)
		}
	})
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if len(out) != n {
		t.Fatalf("inserted %d of %d", len(out), n)
	}
	return out
}

func timelineIDs(page readmodel.Timeline) []string {
	out := make([]string, len(page.Items))
	for i, a := range page.Items {
		out[i] = a.ID
	}
	return out
}

func TestTimelineBeforeWorldTimeLeaksNothingAtOrAfterT(t *testing.T) {
	w := seed(t)
	ctx := context.Background()
	for k := 1; k <= 5; k++ {
		at := w.At(k)
		page, err := reader(t).TimelinePage(ctx, w.Account, readmodel.MaxLimit, &at, "")
		if err != nil {
			t.Fatal(err)
		}
		got := timelineIDs(page)
		if len(got) != k-1 {
			t.Errorf("T=E%d: %d activities, want %d", k, len(got), k-1)
		}
		raw, _ := json.Marshal(page)
		assertNoneOf(t, "timeline before E"+itoa(k), string(raw), ids(w, k, 5)...)
		assertNoneOf(t, "timeline before E"+itoa(k), string(raw), sourceEvents(w, k, 5)...)
	}
}

// More than the page size (and more than the tie cap) of activities after T must not push the cutoff out.
func TestTimelineCutoffSurvivesMoreThanAThousandActivitiesAfterIt(t *testing.T) {
	w := seed(t)
	insertBulk(t, w, "late", 1500, `'2026-09-20T00:00:00Z'::timestamptz + (n.i || ' seconds')::interval`)
	at := w.At(3)
	page, err := reader(t).TimelinePage(context.Background(), w.Account, 50, &at, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := timelineIDs(page); len(got) != 2 || got[0] != w.Event(2).ActivityID || got[1] != w.Event(1).ActivityID {
		t.Fatalf("before E3 want [E2 E1] whatever came later, got %v", got)
	}
	if page.NextBefore != nil || page.NextBeforeID != nil {
		t.Fatalf("a complete page has no cursor: %v %v", page.NextBefore, page.NextBeforeID)
	}
}

// 1500 activities sharing one instant, paged with (before, before_id): no row is dropped or repeated.
func TestTimelinePagesAThousandPlusTiesWithoutLoss(t *testing.T) {
	w := seed(t)
	ctx := context.Background()
	tied := insertBulk(t, w, "tie", 1500, `'2026-09-02T10:00:00Z'::timestamptz`)
	cutoff := w.At(3)
	want := map[string]bool{w.Event(1).ActivityID: true, w.Event(2).ActivityID: true}
	for _, id := range tied {
		want[id] = true
	}
	seen := map[string]bool{}
	var before *time.Time = &cutoff
	beforeID := ""
	for pages := 0; pages < 100; pages++ {
		page, err := reader(t).TimelinePage(ctx, w.Account, 200, before, beforeID)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range timelineIDs(page) {
			if seen[id] {
				t.Fatalf("activity %s returned twice", id)
			}
			seen[id] = true
		}
		if page.NextBefore == nil {
			break
		}
		if page.NextBeforeID == nil {
			t.Fatal("a cursor needs both halves")
		}
		before, beforeID = page.NextBefore, *page.NextBeforeID
	}
	if len(seen) != len(want) {
		t.Fatalf("paged %d activities, want %d (dropped or extra)", len(seen), len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("activity %s was dropped", id)
		}
	}
	for _, id := range ids(w, 3, 5) {
		if seen[id] {
			t.Fatalf("an activity at or after the cutoff was served: %s", id)
		}
	}
}

func TestTimelineBeforeIDNeedsBeforeAndAUUID(t *testing.T) {
	w := seed(t)
	ctx := context.Background()
	at := w.At(3)
	if _, err := reader(t).TimelinePage(ctx, w.Account, 10, nil, w.Event(1).ActivityID); !errors.Is(err, readmodel.ErrInvalid) {
		t.Errorf("before_id without before: %v", err)
	}
	if _, err := reader(t).TimelinePage(ctx, w.Account, 10, &at, "not-a-uuid"); !errors.Is(err, readmodel.ErrInvalid) {
		t.Errorf("malformed before_id: %v", err)
	}
}

func TestTimelineEndpointWorldCutoffAndCursor(t *testing.T) {
	s := newStack(t)
	w := s.world
	const tmpl = "/accounts/{account_id}/timeline"
	base := "/accounts/" + w.Account + "/timeline?limit=2&before="

	r := s.get(base+stamp(w.At(4)), tmpl)
	if r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	assertNoneOf(t, "GET timeline?before=E4", string(r.body), ids(w, 4, 5)...)
	var page struct {
		Items        []struct{ ID string } `json:"items"`
		NextBefore   *string               `json:"next_before"`
		NextBeforeID *string               `json:"next_before_id"`
	}
	if err := json.Unmarshal(r.body, &page); err != nil || len(page.Items) != 2 || page.NextBefore == nil || page.NextBeforeID == nil {
		t.Fatalf("first page = %+v %v", page, err)
	}
	r2 := s.get("/accounts/"+w.Account+"/timeline?limit=2&before="+url.QueryEscape(*page.NextBefore)+"&before_id="+*page.NextBeforeID, tmpl)
	if r2.status != http.StatusOK {
		t.Fatalf("second page %d %s", r2.status, r2.body)
	}
	assertNoneOf(t, "GET timeline second page", string(r2.body), ids(w, 3, 5)...)

	if r := s.get("/accounts/"+w.Account+"/timeline?before_id="+w.Event(1).ActivityID, tmpl); r.status != http.StatusBadRequest {
		t.Errorf("before_id without before: %d", r.status)
	}
	if r := s.get("/accounts/"+w.Account+"/timeline?before="+stamp(w.At(4))+"&before_id=zzz", tmpl); r.status != http.StatusBadRequest {
		t.Errorf("malformed before_id: %d", r.status)
	}
}

// before_id never widens the strict `before`: it must be an activity of the account that occurred exactly at
// `before` (the last row of the previous page). Otherwise rows tied at `before` that no page showed would
// be admitted by the id tie-break.
func TestTimelineBeforeIDCannotWidenTheCutoff(t *testing.T) {
	w := seed(t)
	ctx := context.Background()
	insertBulk(t, w, "widen", 3, `'`+w.At(3).Format(time.RFC3339Nano)+`'::timestamptz`) // ties at E3, not yet shown by any page
	at := w.At(3)
	for name, id := range map[string]string{
		"an unknown id":                     "99999999-9999-4999-8999-999999999999",
		"an activity that is not at before": w.Event(1).ActivityID,
		"a later activity than before":      w.Event(5).ActivityID,
	} {
		if _, err := reader(t).TimelinePage(ctx, w.Account, 50, &at, id); !errors.Is(err, readmodel.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	// Without before_id the ties at before are excluded (strict); with the E3 row as cursor only ties after it by id are served.
	plain, err := reader(t).TimelinePage(ctx, w.Account, 50, &at, "")
	if err != nil {
		t.Fatal(err)
	}
	assertNoneOf(t, "plain", strings.Join(timelineIDs(plain), ","), ids(w, 3, 5)...)
	page, err := reader(t).TimelinePage(ctx, w.Account, 50, &at, w.Event(3).ActivityID)
	if err != nil {
		t.Fatalf("a cursor at its own instant is valid: %v", err)
	}
	assertNoneOf(t, "keyset", strings.Join(timelineIDs(page), ","), ids(w, 4, 5)...)
}
