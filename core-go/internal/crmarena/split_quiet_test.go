package crmarena

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// quietWindows hand-builds deals that ended 10, 59, 60 and 200 days before 2023-11-01, plus a deal that
// is still active at the cutoff.
func quietWindows(t *testing.T) (map[string]Window, time.Time) {
	t.Helper()
	cut := cutoff(t, "2023-11-01")
	day := func(back int) time.Time { return cut.AddDate(0, 0, -back) }
	ended := func(id string, quiet int) Window {
		at := day(quiet)
		return Window{DealID: id, AccountID: "A", Stage: "Quote", Start: day(quiet + 30), Last: at, Activities: 1, times: []time.Time{at}}
	}
	w := map[string]Window{"q10": ended("q10", 10), "q59": ended("q59", 59), "q60": ended("q60", 60), "q200": ended("q200", 200)}
	times := []time.Time{day(40), day(30), cut.AddDate(0, 0, 5), cut.AddDate(0, 0, 9)}
	w["cur"] = Window{DealID: "cur", AccountID: "A", Stage: "Quote", Start: day(40), Last: times[3], Activities: 4, times: times}
	return w, cut
}

func TestQuietDaysAtCutoffIsWholeDaysBeforeIt(t *testing.T) {
	w, cut := quietWindows(t)
	for id, want := range map[string]int{"q10": 10, "q59": 59, "q60": 60, "q200": 200} {
		if got := w[id].QuietDaysAt(cut); got != want {
			t.Errorf("%s quiet days = %d, want %d", id, got, want)
		}
	}
	if got := w["q10"].QuietDaysAt(cut.Add(-36 * time.Hour)); got != 8 {
		t.Errorf("a partial day must round down, got %d", got)
	}
}

func TestDealsInsideTheQuietWindowAreAmbiguousNeverCurrent(t *testing.T) {
	w, _ := quietWindows(t)
	s, err := NewSplitWith(w, SplitConfig{Cutoff: "2023-11-01", PreviousMinQuietDays: 60})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Previous, ","); got != "q200,q60" {
		t.Errorf("previous = %s, want q200,q60 (60 quiet days is enough)", got)
	}
	if got := strings.Join(s.Ambiguous, ","); got != "q10,q59" {
		t.Errorf("ambiguous = %s, want q10,q59", got)
	}
	if len(s.Current) != 1 || s.Current[0] != "cur" {
		t.Errorf("current = %v: an ambiguous deal must never be current", s.Current)
	}
	c := s.Counts
	if c.Previous != 2 || c.Ambiguous != 2 || c.Current != 1 || c.AccountsWithBoth != 1 {
		t.Errorf("counts = %+v", c)
	}
	if s.PreviousMinQuietDays != 60 || s.AmbiguousByStage["Quote"] != 2 {
		t.Errorf("manifest fields: %d, %v", s.PreviousMinQuietDays, s.AmbiguousByStage)
	}
	assertQuiet(t, s.PreviousDeals, map[string]int{"q200": 200, "q60": 60})
	assertQuiet(t, s.AmbiguousDeals, map[string]int{"q10": 10, "q59": 59})
}

func assertQuiet(t *testing.T, got []DealQuiet, want map[string]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("deals = %+v, want %v", got, want)
	}
	for _, d := range got {
		if want[d.DealID] != d.DaysQuiet || d.Stage == "" {
			t.Errorf("deal %+v, want %d quiet days and a stage", d, want[d.DealID])
		}
	}
}

func TestQuietFilterOffRestoresHindsightPartition(t *testing.T) {
	w, _ := quietWindows(t)
	s, err := NewSplitWith(w, SplitConfig{Cutoff: "2023-11-01", PreviousMinQuietDays: 0})
	if err != nil || s.Counts.Previous != 4 || s.Counts.Ambiguous != 0 || len(s.AmbiguousDeals) != 0 {
		t.Fatalf("filter off: %+v, %v", s.Counts, err)
	}
	if _, err := NewSplitWith(w, SplitConfig{Cutoff: "2023-11-01", PreviousMinQuietDays: -1}); err == nil {
		t.Error("negative quiet period accepted")
	}
}

func TestSplitJSONCarriesQuietDaysAndTheFilter(t *testing.T) {
	w, _ := quietWindows(t)
	s, _ := NewSplit(w, "2023-11-01", 0) // the default is 60
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"previous_min_quiet_days":60`, `"days_quiet_at_cutoff":200`, `"ambiguous_deal_ids":["q10","q59"]`, `"ambiguous":2`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("split JSON lacks %s", key)
		}
	}
}
