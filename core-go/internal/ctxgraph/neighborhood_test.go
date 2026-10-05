package ctxgraph

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/visibility"
)

func projected(t *testing.T) (ids, *Reader) {
	t.Helper()
	s := seedWorld(t)
	g := newGraph(t)
	projectAll(t, s, newProjector(t, g))
	return s, NewReader(g, pg.DB, nil)
}

func nodeIDs(v View, section string) map[string]bool {
	out := map[string]bool{}
	for _, id := range v.Sections[section] {
		out[id] = true
	}
	return out
}

func TestNeighborhoodReturnsTheBoundedSectionsWithEvidenceRefs(t *testing.T) {
	s, r := projected(t)
	v, err := r.Neighborhood(bg, s["A"], Params{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Projection.Complete || v.Projection.ProjectedAt == nil {
		t.Errorf("projection = %+v", v.Projection)
	}
	if !nodeIDs(v, SectionAccount)[s["A"]] || !nodeIDs(v, SectionOpportunities)[s["OPP"]] {
		t.Errorf("account/opportunity missing: %v", v.Sections)
	}
	st := nodeIDs(v, SectionStakeholders)
	if !st[s["CHAMP"]] || !st[s["BUYER"]] {
		t.Errorf("stakeholders = %v, want the champion and the economic buyer", st)
	}
	if nodeIDs(v, SectionClaims)[s["C_OLD"]] {
		t.Error("a superseded claim is history, not current")
	}
	if len(v.Sections[SectionSignals]) != 0 {
		t.Error("the event signal's 14-day window ended before now: it is history, not current")
	}
	if !nodeIDs(v, SectionCommitments)[s["C_COMMIT"]] || !nodeIDs(v, SectionKnowledge)[s["K1"]] || !nodeIDs(v, SectionDecisions)[s["EP"]] {
		t.Errorf("commitments/signals/knowledge/decisions missing: %v", v.Sections)
	}
	for _, n := range v.Nodes {
		if n.Type == LabelClaim {
			if len(n.EvidenceRefs) == 0 || n.Data["field_path"] == nil {
				t.Errorf("claim %s lacks evidence refs", n.ID)
			}
			if _, leaked := n.Data["value"]; leaked {
				t.Error("claim values are Postgres' to serve, not the graph's")
			}
		}
	}
	for _, e := range v.Edges {
		if e.Status == "closed" {
			t.Errorf("closed edge %s in the current view", e.ID)
		}
	}
	for _, n := range v.Nodes {
		if n.ID == s["B"] || n.ID == s["BCONTACT"] {
			t.Error("another account leaked into the neighborhood")
		}
	}
}

func TestNeighborhoodHistoryIsOptIn(t *testing.T) {
	s, r := projected(t)
	v, err := r.Neighborhood(bg, s["A"], Params{IncludeClosed: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !nodeIDs(v, SectionClaims)[s["C_OLD"]] || !nodeIDs(v, SectionSignals)[s["SIG"]] {
		t.Error("include_closed must return the superseded claim and the expired signal")
	}
	var closed int
	for _, e := range v.Edges {
		if e.Status == "closed" {
			closed++
		}
	}
	if closed == 0 {
		t.Error("include_closed must return the closed champion edge")
	}
}

func TestNeighborhoodIsBoundedAndSaysSo(t *testing.T) {
	s, r := projected(t)
	v, err := r.Neighborhood(bg, s["A"], Params{SectionLimit: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, ids := range v.Sections {
		if len(ids) > 1 {
			t.Errorf("section %s has %d nodes with a limit of 1", name, len(ids))
		}
	}
	if !v.Truncated {
		t.Error("truncated must be set when a section had more")
	}
	if _, err := r.Neighborhood(bg, s["A"], Params{SectionLimit: 99}, nil); err == nil {
		t.Error("an out-of-range limit was accepted")
	}
	if _, err := r.Neighborhood(bg, "00000000-0000-4000-8000-000000000000", Params{}, nil); err != ErrAccountUnknown {
		t.Errorf("unknown account err = %v", err)
	}
}

func TestNeighborhoodWithholdsWhatTheAgentMayNotSee(t *testing.T) {
	s, r := projected(t)
	hidden, err := visibility.HiddenActivities(bg, pg.DB, s["A"])
	if err != nil || !hidden[s["ACT_HID"]] {
		t.Fatalf("hidden = %v %v", hidden, err)
	}
	v, err := r.Neighborhood(bg, s["A"], Params{}, hidden)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Withheld {
		t.Error("withheld must be set")
	}
	for _, n := range v.Nodes {
		if n.ID == s["ACT_HID"] {
			t.Error("a hidden activity was returned")
		}
		if n.ID == s["C_HID"] && (n.Withheld != WithheldVisibility || n.Label != LabelClaim || len(n.EvidenceRefs) != 0) {
			t.Errorf("claim from a hidden activity must be a stub: %+v", n)
		}
	}
	if !nodeIDs(v, SectionClaims)[s["C_HID"]] {
		t.Error("the stub should keep its place (the claim exists, its content is withheld)")
	}
	for _, e := range v.Edges {
		if e.Source == s["ACT_HID"] || e.Target == s["ACT_HID"] || e.Source == s["C_HID"] {
			t.Errorf("edge %s touches withheld evidence", e.ID)
		}
	}
	// The same call without a hidden set is the operator view and shows it.
	op, err := r.Neighborhood(bg, s["A"], Params{}, nil)
	if err != nil || !nodeIDs(op, SectionActivities)[s["ACT_HID"]] {
		t.Errorf("operator view lost the activity: %v", err)
	}
}

func TestToolItemsAreCompactBoundedAndCarryProjectionStatus(t *testing.T) {
	s, r := projected(t)
	items, truncated, err := r.NeighborhoodItems(bg, s["A"], 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || len(items) > 10 {
		t.Fatalf("items = %d", len(items))
	}
	first := items[0].(map[string]any)
	if first["section"] != SectionAccount || first["projection"] == nil {
		t.Errorf("first item = %v", first)
	}
	if toolSectionLimit(10) != 5 || toolSectionLimit(20) != 10 {
		t.Errorf("section limits %d %d", toolSectionLimit(10), toolSectionLimit(20))
	}
	_ = truncated
}

func TestOpportunityStateRolesAreProjectedFromTheBuyingGroup(t *testing.T) {
	s := seedWorld(t)
	runSeed(t, s, []seedStep{
		{"LEGALX", `INSERT INTO people (kind, display_name, account_id) VALUES ('contact','Lena Legal',$A) RETURNING id`},
	})
	state := `{"account_id":"` + s["A"] + `","opportunity_id":"` + s["OPP"] + `","version":1,"is_open":true,
 "buying_group":[{"person_id":"` + s["LEGALX"] + `","display_name":"Lena","roles":["technical_evaluator"],"status":"active",
   "evidence_refs":[{"activity_id":"` + s["ACT_CALL"] + `","occurred_at":"2026-09-02T10:00:00Z"}]}]}`
	runSeed(t, s, []seedStep{
		{"", `INSERT INTO claims (account_id, opportunity_id, subject_person_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($A,$OPP,$LEGALX,'buying_group.member','{"role":"technical_evaluator"}','first_party_ai',0.75,$ACT_CALL,'Lena evaluates','2026-09-02T10:00:00Z','llm:test@v1')`},
		{"", `INSERT INTO opportunity_state_history (opportunity_id, account_id, version, as_of, state) VALUES ($OPP,$A,1,'2026-09-02T10:00:00Z','` + state + `')`},
		{"", `INSERT INTO opportunity_state (opportunity_id, account_id, version, as_of, is_open, state) VALUES ($OPP,$A,1,'2026-09-02T10:00:00Z',true,'` + state + `')`},
	})
	snap := buildAcme(t, s)
	var found *Edge
	for i, e := range snap.Edges {
		if e.Type == RelTechnicalEvaluator {
			found = &snap.Edges[i]
		}
	}
	if found == nil {
		t.Fatal("no TECHNICAL_EVALUATOR_FOR edge from the opportunity state")
	}
	if found.FromID != s["LEGALX"] || found.ToID != s["OPP"] || found.Props["standing"] != "first_party_ai" || found.Props["confidence"] != 0.75 {
		t.Errorf("edge = %+v", found)
	}
	if err := ValidateSnapshot(snap); err != nil {
		t.Fatal(err)
	}
}
