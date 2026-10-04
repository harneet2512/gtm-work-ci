package signalbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/signals"
)

func TestGoldStateAdapter(t *testing.T) {
	g := goldFile{Account: "acme", Checkpoint: 2, AsOf: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	g.Expected.StateFields = map[string]json.RawMessage{
		"stage":               json.RawMessage(`"Discovery"`),
		"health":              json.RawMessage(`"unknown"`),
		"next_meeting":        json.RawMessage(`null`),
		"blockers":            json.RawMessage(`[{"text":"SOC2","status":"resolved"},{"text":"budget"}]`),
		"current_commitments": json.RawMessage(`[{"text":"send","status":"open","owner":"person:dana","due":"2026-08-28"}]`),
		"objections":          json.RawMessage(`[]`),
	}
	g.Expected.BuyingGroup = []goldMember{{PersonKey: "person:priya", Roles: []string{"champion"}, Status: "active"}}
	g.Expected.CoverageGaps = []string{"economic_buyer"}
	st, err := stateOf(g)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != 2 || !st.Fields.Stage.Known || st.Fields.Health.Known || !st.Fields.NextMeeting.Known || st.Fields.NextMeeting.Value != nil {
		t.Fatalf("scalars wrong: %+v", st.Fields)
	}
	items := st.Fields.Blockers.ListItems()
	if len(items) != 2 || items[0].Status != "resolved" || items[1].Status != "open" {
		t.Fatalf("blockers = %+v", items)
	}
	c := st.Fields.CurrentCommitments.ListItems()
	if len(c) != 1 || c[0].OwnerPersonID != "person:dana" || c[0].DueAt == nil || c[0].DueAt.Day() != 28 {
		t.Fatalf("commitments = %+v", c)
	}
	if st.Fields.Objections.Known || len(st.BuyingGroup) != 1 || len(st.CoverageGaps) != 1 || st.Fields.Summary.Known {
		t.Fatalf("state = %+v", st)
	}
}

func TestGoldStateAdapterErrors(t *testing.T) {
	for name, raw := range map[string]string{"bad json": `{`, "bad due": `[{"text":"x","due":"tomorrow"}]`, "bad items": `[1]`} {
		g := goldFile{}
		g.Expected.StateFields = map[string]json.RawMessage{"blockers": json.RawMessage(raw)}
		if _, err := stateOf(g); err == nil {
			t.Errorf("%s must be an error", name)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("no gold must be an error")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "fixtures", "gold", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "fixtures", "gold", "acme", "cp1.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("unparsable gold must be an error")
	}
	missing := `{"account":"acme","checkpoint":1,"after_event_file":"world/accounts/acme/events/001.json","as_of":"2026-09-01T00:00:00Z","expected":{}}`
	if err := os.WriteFile(bad, []byte(missing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("an after_event_file outside the account's events must be an error")
	}
}

func TestScoreCountsMissesAndExtras(t *testing.T) {
	cp := Checkpoint{Account: "a", Number: 1, Expected: Expected{Signals: []string{"customer_replied", "stage_advanced"}, MaterialDiffFields: []string{"stage"}}}
	cp.Expected.Trigger.Eligible = true
	cp.Expected.Trigger.ReasonCodes = []string{"eligible_customer_replied"}
	res := Result{Checkpoint: cp, Signals: []signals.Signal{{Type: "customer_replied"}, {Type: "expansion_interest"}}}
	res.Diff.IsMaterial = true
	res.Diff.Changes = nil
	rep := Score([]Result{res})
	if rep.Signals.TP != 1 || rep.Signals.FP != 1 || rep.Signals.FN != 1 || *rep.Signals.Precision != 0.5 {
		t.Fatalf("signals = %+v", rep.Signals)
	}
	if rep.SignalsByType["stage_advanced"].FN != 1 || rep.SignalsByType["expansion_interest"].FP != 1 || rep.SignalsByType["customer_replied"].TP != 1 {
		t.Fatalf("by type = %+v", rep.SignalsByType)
	}
	if rep.MaterialFields.FN != 1 || rep.NoMaterialDiff.TP+rep.NoMaterialDiff.FP+rep.NoMaterialDiff.FN != 0 {
		t.Fatalf("material = %+v / %+v", rep.MaterialFields, rep.NoMaterialDiff)
	}
	if rep.Trigger.Cases != 1 || rep.Rows[0].BurstEvents != 0 || len(rep.Rows[0].SignalsExtra) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	empty := score(0, 0, 0)
	if empty.Precision != nil || empty.Recall != nil || empty.F1 != nil {
		t.Fatalf("undefined ratios must be nil: %+v", empty)
	}
}

func TestHistoryRecordsCarryOccurrenceAndWindow(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	exp := at.Add(signals.EventWindow)
	res := []Result{{Checkpoint: Checkpoint{Account: "acme", Number: 3}, Signals: []signals.Signal{
		{Type: "customer_replied", DedupeKey: "k1", OccurredAt: at, ExpiresAt: &exp},
		{Type: "security_blocker_appeared", DedupeKey: "k2", OccurredAt: at}}}}
	recs := HistoryRecords(res)["acme"]
	if len(recs) != 2 || !recs[0].OccurredAt.Equal(at) || recs[0].ExpiresAt == nil || recs[1].ExpiresAt != nil || recs[0].ID == recs[1].ID {
		t.Fatalf("records = %+v", recs)
	}
}
