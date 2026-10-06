package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

func repoPath(t testing.TB, rel string) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("%s not found", rel)
		}
		dir = filepath.Dir(dir)
	}
}

// ---- distinctness: the shared fixture pins Go to the worker's verdicts -------------------------------

func TestDistinctnessMatchesTheSharedFixture(t *testing.T) {
	raw, err := os.ReadFile(repoPath(t, "fixtures/orchestrator/distinctness_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Limit float64 `json:"similarity_limit"`
		Cases []struct {
			Name string `json:"name"`
			A, B struct {
				StrategyType string   `json:"strategy_type"`
				ActionType   string   `json:"action_type"`
				People       []string `json:"people"`
				Body         string   `json:"body"`
			}
			Distinct bool `json:"distinct"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Limit != SimilarityLimit {
		t.Fatalf("fixture limit %v != %v", doc.Limit, SimilarityLimit)
	}
	for _, c := range doc.Cases {
		a := shape{c.A.StrategyType, c.A.ActionType, c.A.People, c.A.Body}
		b := shape{c.B.StrategyType, c.B.ActionType, c.B.People, c.B.Body}
		if why := notDistinct(a, b); (why == "") != c.Distinct {
			t.Errorf("%s: distinct = %v, reason %q", c.Name, why == "", why)
		}
	}
}

// ---- the transition policy ----------------------------------------------------------------------------

func cand(class, action, body string, to ...string) workerclient.Candidate {
	c := workerclient.Candidate{ActionClass: class, ActionType: action}
	for _, id := range to {
		c.To = append(c.To, workerclient.Recipient{PersonID: id, Role: "to"})
	}
	c.FullActionArtifact.Body = body
	return c
}

func TestCandidatePolicyTable(t *testing.T) {
	const calm = "Thanks for the update. Could you tell us when your team has finished reading?"
	cases := []struct {
		name    string
		status  string
		c       workerclient.Candidate
		want    string // "" none, allowed, restricted
		reasons []string
	}{
		{"no transition, no verdict", "", cand("EXPANSION_MOTION", "send_email", calm, "p1"), "", nil},
		{"CANDIDATE restricts an expansion motion", "CANDIDATE", cand("EXPANSION_MOTION", "send_email", calm, "p1"), "restricted", []string{ReasonExpansionMotion}},
		{"UNRESOLVED restricts it too", "UNRESOLVED", cand("EXPANSION_MOTION", "schedule_meeting", calm, "p1"), "restricted", []string{ReasonExpansionMotion}},
		{"CONFIRMED allows it", "CONFIRMED", cand("EXPANSION_MOTION", "send_email", calm, "p1"), "allowed", nil},
		{"REJECTED allows it", "REJECTED", cand("EXPANSION_MOTION", "send_email", calm, "p1"), "allowed", nil},
		{"a low-pressure follow-up is allowed", "CANDIDATE", cand("REPLY", "send_email", calm, "p1"), "allowed", nil},
		{"research is allowed", "CANDIDATE", cand("ASK_RESEARCH", "internal_note", "Which regions come first?", "p9"), "allowed", nil},
		{"waiting is allowed", "CANDIDATE", cand("WAIT", "wait", ""), "allowed", nil},
		{"a pricing push is restricted", "CANDIDATE", cand("REPLY", "send_email", "We can offer a 15% discount on the new seats if you sign.", "p1"), "restricted", []string{ReasonExpansionMotion, ReasonLabelMismatch, ReasonPricingPush, ReasonEscalation}}, // "new seats" is an expansion CTA too
		{"a price quote is restricted", "CANDIDATE", cand("REPLY", "send_email", "Attached is our pricing for 200 seats at $40 per seat.", "p1"), "restricted", []string{ReasonPricingPush}},
		{"commercial escalation is restricted", "CANDIDATE", cand("REPLY", "send_email", "Please send the order form to procurement so we can close this deal.", "p1"), "restricted", []string{ReasonEscalation}},
		{"broad outreach is restricted", "CANDIDATE", cand("REPLY", "send_email", calm, "p1", "p2", "p3", "p4"), "restricted", []string{ReasonBroadOutreach}},
		{"an internal note naming prices is not outreach", "CANDIDATE", cand("INTERNAL_TASK", "internal_note", "Check the pricing sheet.", "p9"), "allowed", nil},
		{"words that merely contain 'sign' are fine", "CANDIDATE", cand("REPLY", "send_email", "The design signals a strong fit; no rush.", "p1"), "allowed", nil},
	}
	for _, c := range cases {
		got := CandidatePolicy(c.status, c.c)
		switch {
		case c.want == "" && got != nil:
			t.Errorf("%s: want no verdict, got %+v", c.name, got)
		case c.want != "" && (got == nil || got.Status != c.want):
			t.Errorf("%s: got %+v, want %s", c.name, got, c.want)
		case got != nil && !slices.Equal(got.Reasons, append([]string{}, c.reasons...)) && c.want != "":
			t.Errorf("%s: reasons %v, want %v", c.name, got.Reasons, c.reasons)
		case got != nil && got.Restricted() != got.RequiresHumanReview:
			t.Errorf("%s: a restricted candidate needs human review: %+v", c.name, got)
		}
	}
}

// ---- ranking: after the evals, blocking and restricted candidates are demoted --------------------------

func ranked(blocking, restricted bool, rank int, name string) evaluated {
	c := cand("REPLY", "send_email", "x", "p1")
	c.StrategyType, c.Ranking = name, rank
	if restricted {
		c.ActionClass = "EXPANSION_MOTION"
	}
	return evaluated{Final: version{Candidate: c, Blocking: blocking}}
}

func TestRankingTable(t *testing.T) {
	cases := []struct {
		name   string
		status string
		in     []evaluated
		want   []string
		none   bool // no acceptable candidate: nothing is preferred
	}{
		{"clean candidates keep the worker's order", "", []evaluated{ranked(false, false, 1, "a"), ranked(false, false, 2, "b"), ranked(false, false, 3, "c")}, []string{"a", "b", "c"}, false},
		{"a blocking favourite drops to last", "", []evaluated{ranked(true, false, 1, "a"), ranked(false, false, 2, "b"), ranked(false, false, 3, "c")}, []string{"b", "c", "a"}, false},
		{"a restricted favourite drops below allowed ones", "CANDIDATE", []evaluated{ranked(false, true, 1, "a"), ranked(false, false, 2, "b"), ranked(false, false, 3, "c")}, []string{"b", "c", "a"}, false},
		{"the same favourite is first under CONFIRMED", "CONFIRMED", []evaluated{ranked(false, true, 1, "a"), ranked(false, false, 2, "b"), ranked(false, false, 3, "c")}, []string{"a", "b", "c"}, false},
		{"blocking ranks below restricted", "CANDIDATE", []evaluated{ranked(true, false, 1, "a"), ranked(false, true, 2, "b"), ranked(false, false, 3, "c")}, []string{"c", "b", "a"}, false},
		{"all blocked: the worker's order stands, nothing is preferred", "", []evaluated{ranked(true, false, 1, "a"), ranked(true, false, 2, "b"), ranked(true, false, 3, "c")}, []string{"a", "b", "c"}, true},
		{"all restricted: still ranked 1 to 3, nothing is preferred", "CANDIDATE", []evaluated{ranked(false, true, 2, "b"), ranked(false, true, 1, "a"), ranked(false, true, 3, "c")}, []string{"a", "b", "c"}, true},
		{"blocked and restricted mixed: nothing is preferred", "CANDIDATE", []evaluated{ranked(true, false, 1, "a"), ranked(false, true, 2, "b"), ranked(false, true, 3, "c")}, []string{"b", "c", "a"}, true},
	}
	for _, c := range cases {
		got, none := rankEvaluated(c.in, c.status)
		if none != c.none {
			t.Errorf("%s: no-acceptable-candidate = %v, want %v", c.name, none, c.none)
		}
		var names []string
		for i, e := range got {
			names = append(names, e.Final.Candidate.StrategyType)
			if e.Final.Candidate.Ranking != i+1 || e.Final.Candidate.PreferredByAgent != (i == 0) {
				t.Errorf("%s: candidate %d has rank %d preferred %v", c.name, i, e.Final.Candidate.Ranking, e.Final.Candidate.PreferredByAgent)
			}
		}
		if !slices.Equal(names, c.want) {
			t.Errorf("%s: order %v, want %v", c.name, names, c.want)
		}
	}
}

// ---- suite routing -------------------------------------------------------------------------------------

func TestRoutingPicksTheSuiteFromStatusStateAndTransition(t *testing.T) {
	r, err := LoadRouting(repoPath(t, "contracts/transitions/routing.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, status, to, from, relationship, want string }{
		{"CANDIDATE expansion", "CANDIDATE", "EXPANSION", "REORG", "", "expansion_candidate"},
		{"CONFIRMED expansion", "CONFIRMED", "EXPANSION", "REORG", "EXPANSION", "expansion_confirmed"},
		{"CANDIDATE reorg is conservative", "CANDIDATE", "REORG", "unknown", "", "conservative"},
		{"CONFIRMED reorg", "CONFIRMED", "REORG", "unknown", "REORG", "reorg_disruption"},
		{"UNRESOLVED is conservative", "UNRESOLVED", "", "EXPANSION", "EXPANSION", "conservative"},
		{"REJECTED falls back to the from_state's suite", "REJECTED", "EXPANSION", "REORG", "REORG", "reorg_disruption"},
		{"REJECTED without a from_state suite is conservative", "REJECTED", "EXPANSION", "RENEWAL", "RENEWAL", "conservative"},
		{"no transition: the confirmed state's suite", "", "", "", "EXPANSION", "expansion_confirmed"},
		{"no transition and a state without a suite", "", "", "", "RENEWAL", ""},
		{"no transition and no state", "", "", "", "", ""},
		{"a confirmed state without a route uses its state suite", "CONFIRMED", "RENEWAL", "unknown", "RENEWAL", ""},
	}
	for _, c := range cases {
		if got := r.Select(c.status, c.to, c.from, c.relationship); got != c.want {
			t.Errorf("%s: suite %q, want %q", c.name, got, c.want)
		}
	}
	excluded := r.Excluded("expansion_candidate")
	if !slices.Contains(excluded, "champion_strength") || !slices.Contains(excluded, "business_case") || slices.Contains(excluded, "expansion_readiness") {
		t.Fatalf("excluded from expansion_candidate = %v", excluded)
	}
	if !slices.Contains(r.Excluded("expansion_confirmed"), "evidence_sufficiency") || len(r.Excluded("")) != 0 {
		t.Fatal("expansion_confirmed excludes the candidate-only evals; no suite excludes nothing")
	}
}

// ---- the validator: any violation of the invariants rejects the whole answer ----------------------------

func validSet() []workerclient.Candidate {
	const (
		p1 = "00000000-0000-4000-8000-000000000001"
		p2 = "00000000-0000-4000-8000-000000000002"
		a1 = "00000000-0000-4000-8000-0000000000a1"
		k1 = "00000000-0000-4000-8000-0000000000c1"
	)
	mk := func(rank int, id, typ, class, action, body string, to, cc []string) workerclient.Candidate {
		subject := "Re: " + typ
		c := workerclient.Candidate{CandidateID: id, StrategyType: typ, Title: "t", Description: "d", Ranking: rank, PreferredByAgent: rank == 1, Rationale: "r",
			EvidenceRefs: []workerclient.EvidenceRef{{ActivityID: a1}}, KnowledgeRefs: []string{k1}, ActionType: action, ActionClass: class,
			FiveQuestions: workerclient.FiveQuestions{WhatChanged: "a", WhyStateChanged: "b", WhatRemainsUnknown: "c", PriorKnowledgeApplies: "d", WhyNextAction: "e"},
			Subject:       &subject, FullActionArtifact: workerclient.Artifact{Channel: "email", Subject: &subject, Body: body}, Preview: "p"}
		for _, id := range to {
			c.To = append(c.To, workerclient.Recipient{PersonID: id, Role: "to"})
		}
		for _, id := range cc {
			c.CC = append(c.CC, workerclient.Recipient{PersonID: id, Role: "cc"})
		}
		return c
	}
	return []workerclient.Candidate{
		mk(1, "00000000-0000-4000-8000-0000000000b1", "send_package", "REPLY", "send_email", "Attached are the security answers; tell us your timing.", []string{p1}, []string{p2}),
		mk(2, "00000000-0000-4000-8000-0000000000b2", "bring_in_lead", "MEETING", "schedule_meeting", "Could our architect walk your engineers through the encryption design?", []string{p1, p2}, nil),
		mk(3, "00000000-0000-4000-8000-0000000000b3", "ask_owner", "ASK_RESEARCH", "internal_note", "Which regions roll out first?", nil, nil),
	}
}

func TestAValidSetHasNoViolations(t *testing.T) {
	if v := shapeViolations(validSet(), map[string]bool{"00000000-0000-4000-8000-0000000000c1": true}); len(v) != 0 {
		t.Fatalf("violations: %v", v)
	}
}

func TestEveryMutationOfAValidSetIsRejected(t *testing.T) {
	applied := map[string]bool{"00000000-0000-4000-8000-0000000000c1": true}
	mutations := map[string]func(cs []workerclient.Candidate) []workerclient.Candidate{
		"two candidates":    func(cs []workerclient.Candidate) []workerclient.Candidate { return cs[:2] },
		"four candidates":   func(cs []workerclient.Candidate) []workerclient.Candidate { return append(cs, cs[0]) },
		"duplicate rank":    func(cs []workerclient.Candidate) []workerclient.Candidate { cs[1].Ranking = 1; return cs },
		"rank out of range": func(cs []workerclient.Candidate) []workerclient.Candidate { cs[2].Ranking = 4; return cs },
		"two preferred":     func(cs []workerclient.Candidate) []workerclient.Candidate { cs[1].PreferredByAgent = true; return cs },
		"none preferred":    func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].PreferredByAgent = false; return cs },
		"duplicate candidate id": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[1].CandidateID = cs[0].CandidateID
			return cs
		},
		"non-uuid candidate id": func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].CandidateID = "x"; return cs },
		"repeated strategy type": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[1].StrategyType = cs[0].StrategyType
			return cs
		},
		"bad strategy type": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[0].StrategyType = "Stronger CTA"
			return cs
		},
		"restyled copy": func(cs []workerclient.Candidate) []workerclient.Candidate { cs[1] = restyle(cs[0], cs[1]); return cs },
		"empty title":   func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].Title = ""; return cs },
		"long description": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[0].Description = strings.Repeat("x", 301)
			return cs
		},
		"class cannot be the action": func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].ActionClass = "WAIT"; return cs },
		"unknown class":              func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].ActionClass = "PUSH"; return cs },
		"empty five-question answer": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[2].FiveQuestions.WhatRemainsUnknown = "  "
			return cs
		},
		"to with the wrong role":    func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].To[0].Role = "cc"; return cs },
		"cc with a non-uuid person": func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].CC[0].PersonID = "p"; return cs },
		"email without a recipient": func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].To = nil; return cs },
		"email on another channel": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[0].FullActionArtifact.Channel = "slack"
			return cs
		},
		"subject differs": func(cs []workerclient.Candidate) []workerclient.Candidate {
			s := "Other"
			cs[0].Subject = &s
			return cs
		},
		"no evidence": func(cs []workerclient.Candidate) []workerclient.Candidate { cs[0].EvidenceRefs = nil; return cs },
		"non-uuid evidence": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[0].EvidenceRefs[0].ActivityID = "a"
			return cs
		},
		"knowledge that does not apply": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[0].KnowledgeRefs = []string{"00000000-0000-4000-8000-0000000000c2"}
			return cs
		},
		"knowledge cited twice": func(cs []workerclient.Candidate) []workerclient.Candidate {
			cs[0].KnowledgeRefs = []string{"00000000-0000-4000-8000-0000000000c1", "00000000-0000-4000-8000-0000000000c1"}
			return cs
		},
	}
	for name, mutate := range mutations {
		cs := mutate(deepCopy(t, validSet()))
		if v := shapeViolations(cs, applied); len(v) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func restyle(orig, copyOf workerclient.Candidate) workerclient.Candidate {
	copyOf.ActionType, copyOf.ActionClass, copyOf.To, copyOf.CC = orig.ActionType, orig.ActionClass, orig.To, orig.CC
	copyOf.FullActionArtifact.Channel = "email"
	copyOf.FullActionArtifact.Body = orig.FullActionArtifact.Body + " Thanks."
	return copyOf
}

func deepCopy(t *testing.T, cs []workerclient.Candidate) []workerclient.Candidate {
	t.Helper()
	raw, err := json.Marshal(cs)
	if err != nil {
		t.Fatal(err)
	}
	var out []workerclient.Candidate
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
