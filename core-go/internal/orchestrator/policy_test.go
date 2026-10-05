package orchestrator

import (
	"slices"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// The label must not bypass the CANDIDATE policy (HAR-128, Opus review of PR #45, item 1): the policy reads the
// text of an external action as well as its action_class.

func TestAMislabelledCandidateCarryingAnExpansionCTAIsRestricted(t *testing.T) {
	ctas := map[string]string{
		"rollout":             "Great news. When can we plan the rollout to your other offices?",
		"roll out":            "Shall we roll out the tool to the whole group this quarter?",
		"extra seats":         "We can set up extra seats for the new hires.",
		"additional licences": "Would you like additional licences for the contractors?",
		"additional licenses": "Would you like additional licenses for the contractors?",
		"new team":            "Let us bring in a new team to the pilot.",
		"new region":          "Which new region should we start with?",
		"another department":  "Could another department benefit from this too?",
		"upgrade":             "Have you considered an upgrade to the enterprise plan?",
		"expand":              "We would love to expand our work together.",
		"expansion":           "Let us discuss the expansion plan on a call.",
		"broader deployment":  "A broader deployment would give your engineers the same view.",
		"pricing":             "Here is our pricing for the larger footprint.",
		"commercial terms":    "Please review the contract terms attached.",
	}
	for name, body := range ctas {
		for _, class := range []string{"REPLY", "MEETING", "SHARE_DOCUMENT"} {
			action := map[string]string{"REPLY": "send_email", "MEETING": "schedule_meeting", "SHARE_DOCUMENT": "share_document"}[class]
			v := CandidatePolicy("CANDIDATE", cand(class, action, body, "p1"))
			if !v.Restricted() || !v.RequiresHumanReview {
				t.Errorf("%s as %s: want restricted, got %+v", name, class, v)
			}
		}
	}
}

func TestTheLabelTextMismatchIsRecordedInTheReasons(t *testing.T) {
	v := CandidatePolicy("CANDIDATE", cand("REPLY", "send_email", "Can we plan a rollout to your other offices?", "p1"))
	if !slices.Contains(v.Reasons, ReasonExpansionMotion) || !slices.Contains(v.Reasons, ReasonLabelMismatch) {
		t.Errorf("reasons %v must name the expansion motion and the label/text mismatch", v.Reasons)
	}
	honest := CandidatePolicy("CANDIDATE", cand("EXPANSION_MOTION", "send_email", "Can we plan a rollout to your other offices?", "p1"))
	if slices.Contains(honest.Reasons, ReasonLabelMismatch) || !slices.Contains(honest.Reasons, ReasonExpansionMotion) {
		t.Errorf("an honestly labelled expansion has no mismatch: %v", honest.Reasons)
	}
	if v := CandidatePolicy("UNRESOLVED", cand("MEETING", "schedule_meeting", "Let us upgrade your plan.", "p1")); !v.Restricted() {
		t.Errorf("UNRESOLVED restricts on the text too: %+v", v)
	}
}

func TestTheExpansionTextOfAnInternalNoteOrAConfirmedTransitionIsNotRestricted(t *testing.T) {
	text := "Ask the owner about the rollout and extra seats."
	if v := CandidatePolicy("CANDIDATE", cand("ASK_RESEARCH", "internal_note", text, "p9")); v.Restricted() {
		t.Errorf("an internal note is research, not a customer CTA: %+v", v)
	}
	if v := CandidatePolicy("CONFIRMED", cand("REPLY", "send_email", text, "p1")); v.Restricted() {
		t.Errorf("a confirmed transition allows it: %+v", v)
	}
}

func TestPlainWordsDoNotTripThePricingOrSignatureRegexes(t *testing.T) {
	for _, body := range []string{
		"That is a good sign for the pilot, no rush on our side.",
		"The design signals a strong fit.",
		"It is a sign of progress that your team replied so quickly.",
		"We resigned ourselves to waiting for the review.",
	} {
		if v := CandidatePolicy("CANDIDATE", cand("REPLY", "send_email", body, "p1")); v.Restricted() {
			t.Errorf("%q must be allowed, got %+v", body, v)
		}
	}
	for _, body := range []string{"Can you sign the order form?", "Once signed we start.", "Please sign off on the contract.", "If you sign this week we keep the rate."} {
		if v := CandidatePolicy("CANDIDATE", cand("REPLY", "send_email", body, "p1")); !slices.Contains(v.Reasons, ReasonEscalation) {
			t.Errorf("%q must still read as commercial escalation, got %+v", body, v)
		}
	}
}

func TestAMislabelledExpansionIsNeverPreferred(t *testing.T) {
	a := ranked(false, false, 1, "a")
	a.Final.Candidate = cand("REPLY", "send_email", "Let us plan a rollout to your other offices.", "p1")
	a.Final.Candidate.StrategyType, a.Final.Candidate.Ranking = "a", 1
	got, none := rankEvaluated([]evaluated{a, ranked(false, false, 2, "b"), ranked(false, false, 3, "c")}, "CANDIDATE")
	if none || got[0].Final.Candidate.StrategyType != "b" || got[2].Final.Candidate.StrategyType != "a" {
		t.Errorf("the mislabelled favourite must drop to last: %v none=%v", names(got), none)
	}
	if !got[2].Policy.Restricted() || got[2].Final.Candidate.PreferredByAgent {
		t.Errorf("it is restricted and not preferred: %+v", got[2].Policy)
	}
}

func names(es []evaluated) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Final.Candidate.StrategyType)
	}
	return out
}

func TestAtLeastOneAllowedCandidateIsRequiredUnderAnOpenTransition(t *testing.T) {
	bad := cand("REPLY", "send_email", "Let us plan a rollout.", "p1")
	good := cand("REPLY", "send_email", "Thanks, whenever you are ready.", "p1")
	if hasAllowed("CANDIDATE", nil) {
		t.Error("an empty set has no allowed candidate")
	}
	if hasAllowed("CANDIDATE", workerCands(bad, bad, bad)) {
		t.Error("three restricted candidates have no allowed one")
	}
	if !hasAllowed("CANDIDATE", workerCands(bad, good, bad)) {
		t.Error("one allowed candidate satisfies the requirement")
	}
	if !hasAllowed("CONFIRMED", workerCands(bad, bad, bad)) || !hasAllowed("", workerCands(bad)) {
		t.Error("with a confirmed transition or none, every candidate is allowed")
	}
}

func workerCands(cs ...workerclient.Candidate) []workerclient.Candidate { return cs }
