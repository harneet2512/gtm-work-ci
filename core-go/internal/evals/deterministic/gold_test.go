package deterministic

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// unscorable lists gold labels the fixtures cannot support, with the reason (never edit the gold).
// Key: "<case id>/<eval type>".
var unscorable = map[string]string{
	"northstar_cp4_k08_misapplied_withholds_meeting/duplicate_action": "the 09-23 email and Drive share the gold says were re-attached are not among the case's activities, so no prior action can be built",
}

type tally struct{ agree, total int }

// goldRun scores every case against the deterministic evals and returns per-eval agreement, the
// false blocks on should_pass cases and the disagreements.
func goldRun(t *testing.T) (map[EvalType]*tally, []string, []string, int, int, adapterUse, []string) {
	t.Helper()
	ids := loadIDs(t)
	per := map[EvalType]*tally{}
	var falseBlocks, disagreements []string
	good, unscored := 0, 0
	var used adapterUse
	var masked []string
	for _, c := range loadCases(t) {
		in, use := caseInputCounted(t, c, ids)
		used.stubs += use.stubs
		used.extended += use.extended
		if use.extended > 0 {
			masked = append(masked, c.ID)
		}
		js := Evaluate(in)
		byType := map[EvalType]Judgment{}
		for _, j := range js {
			byType[j.Result.EvalType] = j
		}
		if c.ShouldPass {
			good++
			if g := Decide(js); g != GateProceed {
				falseBlocks = append(falseBlocks, fmt.Sprintf("%s (%s)", c.ID, blockers(js)))
			}
		}
		for _, e := range c.Expected {
			j, ok := byType[EvalType(e.EvalType)]
			if !ok {
				continue
			}
			if why, skip := unscorable[c.ID+"/"+e.EvalType]; skip {
				unscored++
				_ = why
				continue
			}
			tl := per[EvalType(e.EvalType)]
			if tl == nil {
				tl = &tally{}
				per[EvalType(e.EvalType)] = tl
			}
			tl.total++
			if j.Result.Verdict == e.Verdict && j.Result.Blocking == e.Blocking {
				tl.agree++
			} else {
				disagreements = append(disagreements, fmt.Sprintf("%s %s gold=%s/%v got=%s/%v: %s", c.ID, e.EvalType,
					e.Verdict, e.Blocking, j.Result.Verdict, j.Result.Blocking, j.Result.Reason))
			}
		}
	}
	return per, falseBlocks, disagreements, good, unscored, used, masked
}

func blockers(js []Judgment) string {
	var parts []string
	for _, j := range js {
		for _, f := range j.Findings {
			if f.Blocking {
				parts = append(parts, string(f.Check)+": "+f.Detail)
			}
		}
	}
	return strings.Join(parts, " | ")
}

// TestGoldAgreement is the committed agreement report against fixtures/evals/cases: per-eval
// agreement with the gold deterministic labels (verdict and blocking) and false blocks on the
// should_pass cases. The thresholds are the floor; raise them when agreement improves.
func TestGoldAgreement(t *testing.T) {
	per, falseBlocks, disagreements, good, unscored, used, masked := goldRun(t)
	agree, total := 0, 0
	types := make([]string, 0, len(per))
	for k := range per {
		types = append(types, string(k))
	}
	sort.Strings(types)
	for _, k := range types {
		tl := per[EvalType(k)]
		t.Logf("agreement %-28s %d/%d", k, tl.agree, tl.total)
		agree += tl.agree
		total += tl.total
	}
	for _, d := range disagreements {
		t.Logf("DISAGREE %s", d)
	}
	t.Logf("gold deterministic agreement %d/%d (unscorable %d); false blocks %d of %d should_pass cases",
		agree, total, unscored, len(falseBlocks), good)
	for _, f := range falseBlocks {
		t.Logf("FALSE-BLOCK %s", f)
	}
	t.Logf("adapter derived %d stub activities and extended %d excerpted activities (cases where the fabricated-quote check is masked: %v)",
		used.stubs, used.extended, masked)
	if used.stubs != goldStubs || used.extended != goldExtended {
		t.Errorf("adapter use changed: %d stubs, %d extended; expected %d and %d (update the report in wp17.md)",
			used.stubs, used.extended, goldStubs, goldExtended)
	}
	if total+unscored != 29 {
		t.Fatalf("expected 29 gold deterministic labels, scored %d, unscorable %d", total, unscored)
	}
	if agree < goldAgreementFloor {
		t.Errorf("agreement %d/%d fell below the floor %d", agree, total, goldAgreementFloor)
	}
	if len(falseBlocks) > goldFalseBlockCeiling {
		t.Errorf("%d false blocks on should_pass cases, ceiling %d", len(falseBlocks), goldFalseBlockCeiling)
	}
}

// What the adapter derives across the gold: reported in wp17.md, asserted here so a change is noticed.
const (
	goldStubs    = 11
	goldExtended = 1
)

const (
	goldAgreementFloor    = 28
	goldFalseBlockCeiling = 0
)

// The seven synthetic good emails (past-tense dates, an ROI percentage, "kick off", an unquantified
// "two sites", no opportunity, an email-only workspace) must all proceed.
func TestSyntheticGoodEmailsProceed(t *testing.T) {
	cases := map[string]func() Input{
		"past call and a promise": func() Input {
			in := baseInput()
			in.Draft.FinishedArtifact.Body = "Thanks for the call on September 28, I'll send the summary of the rollout options today."
			return in
		},
		"as discussed on a past date": func() Input {
			in := baseInput()
			in.Draft.FinishedArtifact.Body = "As discussed on September 25, we will share the summary of the rollout options."
			return in
		},
		"customer ROI percentage": func() Input {
			in := baseInput()
			in.Activities[0].Text += " We saw a 30% reduction in tickets."
			in.Draft.EvidenceRefs = append(in.Draft.EvidenceRefs, EvidenceRef{ActivityID: act1, Quote: "We saw a 30% reduction in tickets."})
			in.Draft.FinishedArtifact.Body = "Great to hear you saw a 30% reduction in tickets; here is the summary of the rollout options."
			return in
		},
		"kick off with a percentage": func() Input {
			in := baseInput()
			in.Activities[0].Text += " 80% of the team is onboarded."
			in.Draft.FinishedArtifact.Body = "With 80% of the team onboarded we can kick off phase two; here is the summary of the rollout options."
			return in
		},
		"two sites": func() Input {
			in := baseInput()
			in.Draft.FinishedArtifact.Body = "Here is the summary of the rollout options for your two sites."
			return in
		},
		"no opportunity": func() Input {
			in := baseInput()
			in.OpportunityID, in.State.OpportunityID, in.Opportunities = nil, nil, nil
			return in
		},
		"email-only workspace": func() Input {
			in := baseInput()
			in.Policy.AllowedTools = []string{ToolEmailSend}
			return in
		},
	}
	for name, mk := range cases {
		js := Evaluate(mk())
		if g := Decide(js); g != GateProceed {
			t.Errorf("%s: gate = %s (%s)", name, g, blockers(js))
		}
	}
}
