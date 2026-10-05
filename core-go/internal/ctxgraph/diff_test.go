package ctxgraph

import (
	"testing"
	"time"
)

func findChange(d EventDiff, kind, op, typ, id string) *EventChange {
	for i := range d.Changes {
		c := &d.Changes[i]
		if c.Kind == kind && c.Op == op && c.Type == typ && (id == "" || c.ID == id) {
			return c
		}
	}
	return nil
}

func TestGraphDiffShowsExactlyWhatEventNChanged(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)

	// Event N arrives: an email that introduces Lena (legal), names her as an influencer, and a Postgres
	// adjudication outranks the old stage claim.
	runSeed(t, s, []seedStep{
		se("SE_N", "m9", 9, "{}"),
		{"ACT_N", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, provenance)
 VALUES ($SE_N,'EmailReceived','email','m9','2026-09-10T10:00:00Z',$A,$OPP,'{"source_system":"email","source_object_id":"m9"}') RETURNING id`},
		{"LENA", `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact','Lena Legal','lena@acme.com',$A) RETURNING id`},
		rel("activity", "$ACT_N", "involves", "person", "$LENA", "first_party_record", 1, "ACT_N", ""),
		rel("activity", "$ACT_N", "about", "opportunity", "$OPP", "first_party_record", 1, "ACT_N", ""),
		rel("person", "$LENA", "works_at", "account", "$A", "crm_explicit", 0.9, "ACT_N", ""),
		rel("person", "$LENA", "influences", "opportunity", "$OPP", "first_party_ai", 0.7, "ACT_N", ""),
		{"C_N", `INSERT INTO claims (account_id, opportunity_id, subject_person_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($A,$OPP,$LENA,'stakeholder_role','{"role":"legal"}','first_party_ai',0.8,$ACT_N,'looping in Lena from legal','2026-09-10T10:00:00Z','llm:test@v1') RETURNING id`},
		{"", `UPDATE claims SET status = 'outranked' WHERE id = $C_STAGE`},
	})
	now := time.Now()
	if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_N"]}, ReasonIngest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Drain(bg); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_N"]}, ReasonRecompute, now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Drain(bg); err != nil {
		t.Fatal(err)
	}

	d, err := DiffForEvent(bg, pg.DB, s["SE_N"])
	if err != nil {
		t.Fatal(err)
	}
	if !d.Projected || len(d.JobIDs) < 1 {
		t.Fatalf("event not projected: %+v", d)
	}
	for _, want := range []struct{ kind, op, typ, id string }{
		{"node", OpAdded, LabelConversation, s["ACT_N"]},
		{"node", OpAdded, LabelPerson, s["LENA"]},
		{"node", OpAdded, LabelClaim, s["C_N"]},
		{"edge", OpAdded, RelInvolves, ""},
		{"edge", OpAdded, RelInfluences, ""},
		{"edge", OpAdded, RelWorksAt, ""},
		{"edge", OpAdded, RelSupportedBy, ""},
		{"node", OpChanged, LabelClaim, s["C_STAGE"]},
	} {
		if findChange(d, want.kind, want.op, want.typ, want.id) == nil {
			t.Errorf("diff misses %s %s %s %s", want.op, want.kind, want.typ, want.id)
		}
	}
	changed := findChange(d, "node", OpChanged, LabelClaim, s["C_STAGE"])
	if changed != nil {
		pc, ok := changed.Changed["status"]
		if !ok || pc.Before != "active" || pc.After != "outranked" {
			t.Errorf("changed claim props = %+v, want status active -> outranked", changed.Changed)
		}
	}
	if c := findChange(d, "node", OpAdded, LabelConversation, s["ACT_N"]); c != nil && !c.AttributedToEvent {
		t.Error("the new activity must be attributed to its own event")
	}
	for _, c := range d.Changes {
		if c.Kind == "node" && (c.ID == s["A"] || c.ID == s["OPP"] || c.ID == s["CHAMP"]) {
			t.Errorf("untouched %s %s appears in the diff as %s", c.Type, c.ID, c.Op)
		}
	}
	// The outranked claim changed with its three derived edges (they carry the claim status).
	sum := d.Summary
	if sum[OpAdded] < 7 || sum[OpChanged] != 4 || sum[OpRemoved] != 0 {
		t.Errorf("summary = %v", sum)
		for _, c := range d.Changes {
			if c.Op == OpChanged {
				t.Logf("changed %s %s %s %v", c.Kind, c.Type, c.ID, c.Changed)
			}
		}
	}

	// Removal: Postgres drops the economic-buyer relationship; the next projection of the event's account removes the edge.
	if _, err := pg.DB.Exec(`DELETE FROM relationships WHERE rel_type = 'economic_buyer_for'`); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_N"]}, ReasonRecompute, now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Drain(bg); err != nil {
		t.Fatal(err)
	}
	if d, err = DiffForEvent(bg, pg.DB, s["SE_N"]); err != nil {
		t.Fatal(err)
	}
	if findChange(d, "edge", OpRemoved, RelEconomicBuyerFor, "") == nil {
		t.Errorf("removed edge missing from the merged diff: %+v", d.Summary)
	}
	if rep, err := p.Drift(bg, ""); err != nil || rep.Drift != 0 {
		t.Fatalf("drift after the event = %+v %v", rep, err)
	}
}

func TestMergeDiffsFoldsSuccessiveProjectionsIntoTheNetChange(t *testing.T) {
	added := Change{Kind: "node", Op: OpAdded, Type: LabelClaim, ID: "c1", AfterHash: "h1", Props: map[string]any{"status": "active"}}
	changed := Change{Kind: "node", Op: OpChanged, Type: LabelClaim, ID: "c1", BeforeHash: "h1", AfterHash: "h2",
		Changed: map[string]PropChange{"status": {Before: "active", After: "superseded"}}}
	removed := Change{Kind: "node", Op: OpRemoved, Type: LabelClaim, ID: "c1", BeforeHash: "h2"}

	got := MergeDiffs(Diff{Changes: []Change{added}}, Diff{Changes: []Change{changed}})
	if len(got.Changes) != 1 || got.Changes[0].Op != OpAdded || got.Changes[0].Props["status"] != "superseded" {
		t.Fatalf("added then changed = %+v, want one add with the final properties", got.Changes)
	}
	if got := MergeDiffs(Diff{Changes: []Change{added}}, Diff{Changes: []Change{changed}}, Diff{Changes: []Change{removed}}); len(got.Changes) != 0 {
		t.Fatalf("added then removed must vanish: %+v", got.Changes)
	}
	back := Change{Kind: "node", Op: OpChanged, Type: LabelClaim, ID: "c1", BeforeHash: "h2", AfterHash: "h1",
		Changed: map[string]PropChange{"status": {Before: "superseded", After: "active"}}}
	first := Change{Kind: "node", Op: OpChanged, Type: LabelClaim, ID: "c1", BeforeHash: "h1", AfterHash: "h2",
		Changed: map[string]PropChange{"status": {Before: "active", After: "superseded"}}}
	if got := MergeDiffs(Diff{Changes: []Change{first}}, Diff{Changes: []Change{back}}); len(got.Changes) != 0 {
		t.Fatalf("a change undone later must vanish: %+v", got.Changes)
	}
}

func TestDiffForAnEventNoProjectionHasSeenIsNotProjected(t *testing.T) {
	s := seedWorld(t)
	d, err := DiffForEvent(bg, pg.DB, s["SE_MAIL"])
	if err != nil {
		t.Fatal(err)
	}
	if d.Projected || len(d.Changes) != 0 {
		t.Fatalf("diff = %+v", d)
	}
}
