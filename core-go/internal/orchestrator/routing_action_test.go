package orchestrator

import "testing"

func TestRoutingRefinesTheSuitePerCandidateByItsAction(t *testing.T) {
	r, err := LoadRouting(repoPath(t, "contracts/transitions/routing.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, status, to, from, rel, class, want string
	}{
		{"an expansion motion under a CANDIDATE reorg gets the readiness suite", "CANDIDATE", "REORG", "EXPANSION", "EXPANSION", "EXPANSION_MOTION", "expansion_candidate"},
		{"a low-pressure reply under the same transition keeps the conservative suite", "CANDIDATE", "REORG", "EXPANSION", "EXPANSION", "REPLY", "conservative"},
		{"UNRESOLVED expansion motion is refined too", "UNRESOLVED", "EXPANSION", "REORG", "REORG", "EXPANSION_MOTION", "expansion_candidate"},
		{"a confirmed expansion motion keeps the confirmed suite", "CONFIRMED", "EXPANSION", "REORG", "EXPANSION", "EXPANSION_MOTION", "expansion_confirmed"},
		{"research under a candidate expansion keeps the route's suite", "CANDIDATE", "EXPANSION", "REORG", "REORG", "ASK_RESEARCH", "expansion_candidate"},
		{"no transition: the state suite, whatever the action", "", "", "", "REORG", "EXPANSION_MOTION", "reorg_disruption"},
		{"no transition and no state suite", "", "", "", "NEW_LOGO", "REPLY", ""},
	}
	for _, c := range cases {
		if got := r.SelectFor(c.status, c.to, c.from, c.rel, c.class); got != c.want {
			t.Errorf("%s: suite %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAnActionRouteMustNameAKnownSuite(t *testing.T) {
	doc := `{"routing_version":"transition_routing:v1","routes":[{"status":"CANDIDATE","to_state":"*","suite":"s"}],
 "action_routes":[{"statuses":["CANDIDATE"],"action_class":"EXPANSION_MOTION","suite":"nope"}],
 "state_suites":{},"suites":{"s":{"evals":[{"eval_types":["evidence_sufficiency"]}]}}}`
	if _, err := ParseRouting([]byte(doc)); err == nil {
		t.Fatal("an action route naming an unknown suite must be refused")
	}
}

func TestTheEffectiveActionClassReadsTheTextOfAnExternalAction(t *testing.T) {
	if got := effectiveActionClass(cand("REPLY", "send_email", "Can we plan a rollout to your other offices?", "p1")); got != "EXPANSION_MOTION" {
		t.Errorf("a mislabelled expansion CTA is routed as an expansion motion, got %s", got)
	}
	if got := effectiveActionClass(cand("REPLY", "send_email", "Thanks, whenever you are ready.", "p1")); got != "REPLY" {
		t.Errorf("got %s", got)
	}
	if got := effectiveActionClass(cand("ASK_RESEARCH", "internal_note", "Check the rollout dates.", "p9")); got != "ASK_RESEARCH" {
		t.Errorf("an internal note keeps its class, got %s", got)
	}
}
