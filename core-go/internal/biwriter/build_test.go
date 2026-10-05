package biwriter_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
)

const (
	acct   = "0a0c0000-0000-4000-8000-000000000001"
	opp    = "0c0f0000-0000-4000-8000-000000000004"
	actN   = "0ac70000-0000-4000-8000-000000000101"
	actOld = "0ac70000-0000-4000-8000-000000000100"
	marco  = "0b0e0000-0000-4000-8000-000000000018"
	priya  = "0b0e0000-0000-4000-8000-000000000017"
	diffID = "0d1f0000-0000-4000-8000-000000000301"
	srcEv  = "05ce0000-0000-4000-8000-000000000001"
	heldID = "0e7e0000-0000-4000-8000-000000000002"
)

var (
	t0  = time.Date(2026, 9, 29, 15, 42, 0, 0, time.UTC)
	now = time.Date(2026, 9, 29, 15, 42, 40, 0, time.UTC)
	ids = biwriter.IDs{Change: "0acc0000-0000-4000-8000-000000000801", BI: "0b100000-0000-4000-8000-000000000a01"}
)

func ev(activity, quote string) biwriter.EvidenceRef {
	at := t0
	return biwriter.EvidenceRef{ActivityID: activity, Quote: quote, SpeakerPersonID: marco, OccurredAt: &at}
}

func entry(field, op string, before, after any, refs ...biwriter.EvidenceRef) biwriter.DiffEntry {
	return biwriter.DiffEntry{Field: field, Op: op, Before: before, After: after, Material: true, EvidenceRefs: refs}
}

// members is a buying-group value as state_diffs.changes holds it after a JSON round trip.
func members(m ...map[string]any) []any {
	out := make([]any, 0, len(m))
	for _, x := range m {
		out = append(out, x)
	}
	return out
}

func member(id, status string, roles ...string) map[string]any {
	r := make([]any, 0, len(roles))
	for _, x := range roles {
		r = append(r, x)
	}
	return map[string]any{"person_id": id, "roles": r, "status": status}
}

func items(texts ...string) []any {
	out := make([]any, 0, len(texts))
	for _, x := range texts {
		out = append(out, map[string]any{"text": x, "claim_id": "c"})
	}
	return out
}

// facts is the SOC2 case: Marco joins the buying group, the blocker changes, the meeting disappears.
func facts() biwriter.Facts {
	return biwriter.Facts{
		AccountID: acct, AccountName: "Acme Corp", OpportunityID: opp, HeldOutEventID: heldID,
		Activity: biwriter.Activity{ID: actN, Type: "EmailReceived", Summary: "Re: Expansion to EU teams", OccurredAt: t0},
		Diff: biwriter.StateDiff{
			ID: diffID, AccountID: acct, FromVersion: 6, ToVersion: 7, IsMaterial: true, ActivityIDs: []string{actN},
			Entries: []biwriter.DiffEntry{
				entry("buying_group", "changed", members(member(priya, "active", "champion")),
					members(member(priya, "active", "champion"), member(marco, "new", "technical_evaluator")), ev(actN, "I lead security at Acme")),
				entry("blockers", "changed", items("budget approval"), items("SOC2 Type II report", "pen-test summary"),
					ev(actN, "we'll need your SOC2 Type II report")),
				entry("next_meeting", "became_unknown", "2026-09-30", nil, ev(actN, "I can't commit to a review date")),
				{Field: "summary", Op: "changed", Before: "a", After: "b", Material: false},
			},
		},
		Graph: biwriter.GraphDiff{SourceEventID: srcEv, JobIDs: []int64{41, 42}, PrevJobID: 37, Items: []biwriter.GraphItem{
			{Kind: "node", Type: "Person", ID: marco, Op: "added", Attributed: true, ActivityIDs: []string{actN}},
			{Kind: "edge", Type: "WORKS_AT", ID: "e1", Op: "added", Attributed: true, ActivityIDs: []string{actN}},
			{Kind: "node", Type: "Claim", ID: "k1", Op: "added", Attributed: true, ActivityIDs: []string{actN}},
			{Kind: "node", Type: "Person", ID: priya, Op: "changed", Attributed: false, ActivityIDs: []string{actOld}},
		}},
		Signals: []biwriter.Signal{{ID: "s1", Type: "security_blocker_appeared"}, {ID: "s2", Type: "new_stakeholder_entered"}},
		Trigger: &biwriter.Trigger{Eligible: true, ReasonCodes: []string{"eligible_blocker_change", "eligible_stakeholder_change"}},
		People:  map[string]string{marco: "Marco Ruiz", priya: "Priya Shah"},
	}
}

func build(t *testing.T, f biwriter.Facts) biwriter.Result {
	t.Helper()
	r, err := biwriter.Build(f, ids, now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return r
}

func TestEveryClaimCitesWhatItRestsOn(t *testing.T) {
	withTransition := facts()
	withTransition.Diff.Entries = append(withTransition.Diff.Entries,
		entry("open_transition", "set", nil, "CANDIDATE:REORG>EXPANSION"))
	withTransition.Transition = &biwriter.Transition{ID: "0e5a0000-0000-4000-8000-000000000002", Status: "CANDIDATE", FromState: "REORG",
		ToState: ptr("EXPANSION"), Supporting: []biwriter.Fact{{Key: "expansion_need_stated", Description: "d", Required: true, Evidence: []biwriter.EvidenceRef{ev(actN, "EU rollout")}}}}

	noEvidence := facts()
	noEvidence.Diff.Entries = []biwriter.DiffEntry{entry("stage", "changed", "Discovery", "Negotiation")}

	for name, f := range map[string]biwriter.Facts{"soc2 case": facts(), "with transition": withTransition, "entry without evidence": noEvidence} {
		t.Run(name, func(t *testing.T) {
			r := build(t, f)
			if r.BI == nil || len(r.BI.Claims) == 0 {
				t.Fatalf("a material change must produce claims: %+v", r.BI)
			}
			for i, c := range r.BI.Claims {
				if len(c.EvidenceRefs) == 0 {
					t.Errorf("claim %d (%q) cites nothing", i, c.Statement)
				}
				for _, ref := range c.EvidenceRefs {
					if ref.ActivityID == "" {
						t.Errorf("claim %d cites an evidence ref without an activity id", i)
					}
				}
				if c.StateDiffField == nil || *c.StateDiffField == "" {
					t.Errorf("claim %d does not name the diff entry it restates", i)
				}
			}
			if err := biwriter.Validate(f, r); err != nil {
				t.Fatalf("Validate rejects Build's own output: %v", err)
			}
		})
	}
}

func TestClaimsRestateOnlyMaterialDiffEntries(t *testing.T) {
	r := build(t, facts())
	var fields []string
	for _, c := range r.BI.Claims {
		fields = append(fields, *c.StateDiffField)
	}
	if want := []string{"buying_group", "blockers", "next_meeting"}; !reflect.DeepEqual(fields, want) {
		t.Fatalf("claim fields = %v, want %v (the non-material summary entry must not be reported)", fields, want)
	}
}

func TestStatementsAreTemplatesOverTheCitedFacts(t *testing.T) {
	r := build(t, facts())
	got := map[string]string{}
	dims := map[string]string{}
	for _, c := range r.BI.Claims {
		got[*c.StateDiffField], dims[*c.StateDiffField] = c.Statement, c.Dimension
	}
	for field, parts := range map[string][]string{
		"buying_group": {"Buying group changed", "added Marco Ruiz (technical_evaluator)"},
		"blockers":     {"Blockers changed from", `"budget approval"`, `"SOC2 Type II report"; "pen-test summary"`},
		"next_meeting": {"Next meeting is no longer known", `"2026-09-30"`},
	} {
		for _, p := range parts {
			if !strings.Contains(got[field], p) {
				t.Errorf("%s statement %q lacks %q", field, got[field], p)
			}
		}
	}
	for field, dim := range map[string]string{"buying_group": "stakeholder_structure", "blockers": "blockers_risk", "next_meeting": "next_step_commitment"} {
		if dims[field] != dim {
			t.Errorf("%s dimension = %s, want %s", field, dims[field], dim)
		}
	}
}

func TestBuyingGroupStatementNamesRemovedAndUpdatedMembers(t *testing.T) {
	f := facts()
	f.Diff.Entries = []biwriter.DiffEntry{entry("buying_group", "changed",
		members(member(priya, "active", "champion"), member(marco, "active", "technical_evaluator")),
		members(member(priya, "weakening", "champion")), ev(actN, "q"))}
	r := build(t, f)
	s := r.BI.Claims[0].Statement
	for _, p := range []string{"removed Marco Ruiz", "Priya Shah", "status active -> weakening"} {
		if !strings.Contains(s, p) {
			t.Errorf("statement %q lacks %q", s, p)
		}
	}
}

func TestAnUnknownPersonIsNamedByIDNotInvented(t *testing.T) {
	f := facts()
	f.People = nil
	r := build(t, f)
	if s := r.BI.Claims[0].Statement; !strings.Contains(s, "person 0b0e0000") {
		t.Fatalf("statement %q must fall back to the person id", s)
	}
}

func TestChangeAndUpdateAreDeterministic(t *testing.T) {
	a, _ := json.Marshal(build(t, facts()))
	b, _ := json.Marshal(build(t, facts()))
	if string(a) != string(b) {
		t.Fatalf("two builds of the same facts differ:\n%s\n%s", a, b)
	}
}

func TestChangeRestatesTheDiffAndTheGraphDiff(t *testing.T) {
	c := build(t, facts()).Change
	if c.ID != ids.Change || c.AccountID != acct || *c.OpportunityID != opp || *c.HeldOutEventID != heldID {
		t.Fatalf("identity fields: %+v", c)
	}
	if c.StateDiffID != diffID || !c.MaterialChange || !reflect.DeepEqual(c.TriggerActivityIDs, []string{actN}) {
		t.Fatalf("diff fields: %+v", c)
	}
	if c.PreviousStateRef.Version != 6 || c.CurrentStateRef.Version != 7 || c.PreviousStateRef.OpportunityID != nil || c.CurrentStateRef.OpportunityID != nil {
		t.Fatalf("state refs must be account-level and equal the diff's versions: %+v %+v", c.PreviousStateRef, c.CurrentStateRef)
	}
	if want := (biwriter.GraphDiffRef{ID: srcEv, FromProjectionSeq: 37, ToProjectionSeq: 42}); c.GraphDiffRef != want {
		t.Fatalf("graph diff ref = %+v, want %+v", c.GraphDiffRef, want)
	}
	if len(c.EvidenceRefs) != 3 {
		t.Fatalf("a material change carries the evidence of its claims: %+v", c.EvidenceRefs)
	}
}

func TestANonMaterialDiffWritesTheChangeButNoUpdate(t *testing.T) {
	f := facts()
	f.Diff.IsMaterial = false
	f.Diff.Entries = []biwriter.DiffEntry{{Field: "summary", Op: "changed", Before: "a", After: "b"}}
	r := build(t, f)
	if r.BI != nil {
		t.Fatalf("nothing material changed, nothing to report: %+v", r.BI)
	}
	if r.Change.MaterialChange || len(r.Change.EvidenceRefs) != 1 || r.Change.EvidenceRefs[0].ActivityID != actN {
		t.Fatalf("change = %+v", r.Change)
	}
}

func TestSummaryAndWhyItMattersAreBuiltFromTheCitedFacts(t *testing.T) {
	f := facts()
	f.Transition = &biwriter.Transition{ID: "0e5a0000-0000-4000-8000-000000000002", Status: "CANDIDATE", FromState: "REORG", ToState: ptr("EXPANSION"), Touched: true,
		Missing: []biwriter.Fact{{Key: "owner_stabilized", Description: "d1", Required: true}, {Key: "economic_buyer_known", Description: "d2", Required: false}}}
	bi := build(t, f).BI
	for _, p := range []string{"Acme Corp", "3 material changes", "an inbound email", "Re: Expansion to EU teams", "2026-09-29", "Buying group", "Blockers"} {
		if !strings.Contains(bi.Summary, p) {
			t.Errorf("summary %q lacks %q", bi.Summary, p)
		}
	}
	for _, p := range []string{"The buying group moved", "added Marco Ruiz (technical_evaluator)", "The risk picture moved", `Blockers moved from "budget approval"`,
		"The next step moved", "Next meeting no longer known",
		"The relationship may be moving from REORG to EXPANSION, but that is not confirmed", "needed: owner stabilized", "helpful: economic buyer known"} {
		if !strings.Contains(bi.WhyItMatters, p) {
			t.Errorf("why_it_matters %q lacks %q", bi.WhyItMatters, p)
		}
	}
	if bi.Model != nil {
		t.Errorf("the writer uses no model: %v", *bi.Model)
	}
	if len(bi.KnowledgeRefs) != 0 {
		t.Errorf("knowledge_refs = %v, none is checked here", bi.KnowledgeRefs)
	}
}

func TestTransitionStatusIsNoneCandidateOrConfirmed(t *testing.T) {
	none := build(t, facts()).BI
	if none.Transition != nil || strings.Contains(none.WhyItMatters, "The relationship") {
		t.Fatalf("none: transition=%+v why=%q", none.Transition, none.WhyItMatters)
	}

	f := facts()
	f.Transition = &biwriter.Transition{ID: "t1", Status: "CANDIDATE", FromState: "REORG", ToState: ptr("EXPANSION"), Touched: true,
		Missing: []biwriter.Fact{{Key: "owner_stabilized", Description: "A champion is stable.", Required: true}}}
	cand := build(t, f).BI.Transition
	if cand == nil || cand.Status != "CANDIDATE" || cand.StateTransitionID != "t1" || cand.FromState != "REORG" || *cand.ToStateCandidate != "EXPANSION" ||
		len(cand.MissingFacts) != 1 || cand.MissingFacts[0].Key != "owner_stabilized" || !cand.MissingFacts[0].Required || cand.MissingFacts[0].Description != "A champion is stable." {
		t.Fatalf("candidate transition = %+v", cand)
	}

	f.Transition = &biwriter.Transition{ID: "t2", Status: "CONFIRMED", FromState: "REORG", ToState: ptr("EXPANSION"), Touched: true}
	conf := build(t, f).BI
	if conf.Transition.Status != "CONFIRMED" || len(conf.Transition.MissingFacts) != 0 || !strings.Contains(conf.WhyItMatters, "The relationship moved from REORG to EXPANSION") {
		t.Fatalf("confirmed transition = %+v / %q", conf.Transition, conf.WhyItMatters)
	}
	if conf.Transition.MissingFacts == nil {
		t.Fatal("missing_facts must serialize as [] not null")
	}
}

func TestTransitionEntriesAreCitedByTheTransitionsOwnEvidence(t *testing.T) {
	f := facts()
	f.Diff.Entries = []biwriter.DiffEntry{entry("relationship_state", "changed", "REORG", "EXPANSION")}
	f.Transition = &biwriter.Transition{ID: "t2", Status: "CONFIRMED", FromState: "REORG", ToState: ptr("EXPANSION"),
		Supporting: []biwriter.Fact{{Key: "k", Description: "d", Required: true, Evidence: []biwriter.EvidenceRef{ev(actOld, "older"), ev(actN, "newer"), ev(actN, "newer")}}}}
	refs := build(t, f).BI.Claims[0].EvidenceRefs
	if len(refs) != 2 || refs[0].ActivityID != actOld || refs[1].ActivityID != actN {
		t.Fatalf("refs = %+v, want the transition's two distinct supporting refs", refs)
	}
}

func TestGraphItemsAttachToTheClaimsOfTheirDimension(t *testing.T) {
	r := build(t, facts())
	byField := map[string][]biwriter.GraphDiffItem{}
	for _, c := range r.BI.Claims {
		byField[*c.StateDiffField] = c.GraphDiffItems
	}
	want := []biwriter.GraphDiffItem{{Kind: "edge", Type: "WORKS_AT", ID: "e1"}, {Kind: "node", Type: "Person", ID: marco}}
	if !reflect.DeepEqual(byField["buying_group"], want) {
		t.Fatalf("buying_group graph items = %+v, want %+v (attributed Person node and WORKS_AT edge only, sorted)", byField["buying_group"], want)
	}
	if got := byField["blockers"]; len(got) != 1 || got[0].Type != "Claim" {
		t.Fatalf("blockers graph items = %+v, want the Claim node", got)
	}
	if got := byField["next_meeting"]; len(got) != 0 {
		t.Fatalf("next_meeting graph items = %+v, want none", got)
	}
}

func TestAGraphItemOfAnotherActivityDoesNotAttach(t *testing.T) {
	f := facts()
	f.Graph.Items = []biwriter.GraphItem{{Kind: "node", Type: "Person", ID: priya, Op: "added", Attributed: true, ActivityIDs: []string{actOld}}}
	for _, c := range build(t, f).BI.Claims {
		if len(c.GraphDiffItems) != 0 {
			t.Fatalf("claim %q cites a graph item that rests on a different activity: %+v", c.Statement, c.GraphDiffItems)
		}
	}
}

func TestGraphDiffRefSpansTheAccountsJobsOfTheEvent(t *testing.T) {
	f := facts()
	f.Graph.JobIDs, f.Graph.PrevJobID = []int64{9}, 4 // jobs 5..8 are other accounts'
	if got := build(t, f).Change.GraphDiffRef; got.FromProjectionSeq != 4 || got.ToProjectionSeq != 9 {
		t.Fatalf("ref = %+v, want the account's previous job (4) .. 9, not job-1", got)
	}
	f.Graph.JobIDs, f.Graph.PrevJobID = []int64{7, 3, 12}, 0 // the account's first job
	if got := build(t, f).Change.GraphDiffRef; got.FromProjectionSeq != 0 || got.ToProjectionSeq != 12 {
		t.Fatalf("ref = %+v, want 0 .. max", got)
	}
}

func ptr[T any](v T) *T { return &v }
