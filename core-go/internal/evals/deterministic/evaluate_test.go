package deterministic

import (
	"encoding/json"
	"testing"
)

func TestEvaluateRunsAllSevenEvalsOnACleanDraft(t *testing.T) {
	js := Evaluate(baseInput())
	if len(js) != 7 {
		t.Fatalf("got %d judgments, want 7", len(js))
	}
	seen := map[EvalType]bool{}
	for _, r := range Results(js) {
		if r.Verdict != "pass" || r.Blocking || r.Kind != "deterministic" || r.Model != nil || r.EvidenceClass != "product_rule" {
			t.Fatalf("clean draft result = %+v", r)
		}
		seen[r.EvalType] = true
		expectContractValid(t, r)
	}
	if len(seen) != 7 || Decide(js) != GateProceed {
		t.Fatalf("types = %v, gate = %s", seen, Decide(js))
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	in := baseInput()
	in.Draft.FinishedArtifact.Body = "Total is $9,999 for 12 seats, 40% off."
	a, _ := json.Marshal(Results(Evaluate(in)))
	b, _ := json.Marshal(Results(Evaluate(in)))
	if string(a) != string(b) {
		t.Fatal("two evaluations of the same input differ")
	}
}

func TestDecide(t *testing.T) {
	revise := baseInput()
	revise.Draft.Recipients = []Recipient{{PersonID: "p-404", Role: "to"}}
	if g := Decide(Evaluate(revise)); g != GateRevise {
		t.Fatalf("gate = %s, want revise", g)
	}
	stop := baseInput()
	stop.Policy.AutonomyLevel = "suggest_only"
	if g := Decide(Evaluate(stop)); g != GateStop {
		t.Fatalf("gate = %s, want stop", g)
	}
	soft := baseInput()
	soft.Draft.FinishedArtifact.Body = priorEmailBody
	soft.PriorActions = []PriorAction{priorEmail(3*24*3600*1e9, priorEmailBody)}
	if g := Decide(Evaluate(soft)); g != GateProceed {
		t.Fatalf("a non-blocking failure must proceed, got %s", g)
	}
}

func TestResultIDsFollowTheInput(t *testing.T) {
	a := Results(Evaluate(baseInput()))
	b := Results(Evaluate(baseInput()))
	changed := baseInput()
	changed.Draft.FinishedArtifact.Body += " Thanks again."
	c := Results(Evaluate(changed))
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Errorf("%s: the same input must give the same id", a[i].EvalType)
		}
		if a[i].ID == c[i].ID {
			t.Errorf("%s: a changed input must change the id", a[i].EvalType)
		}
	}
}

func TestUnreadableStateIsAnExplicitFindingNotAPass(t *testing.T) {
	t.Run("stage that is not text", func(t *testing.T) {
		in := crmInput("commercial review")
		in.State.Fields.Stage = known(42)
		expectFinding(t, StageUpdateLegal(in), CheckStageLegal, true, "unreadable")
		expectFinding(t, CRMActionNotAlreadyCompleted(in), CheckCRMNotCompleted, true, "unreadable")
	})
	t.Run("owner that is not text", func(t *testing.T) {
		in := crmInput("commercial review")
		in.Opportunities = nil
		in.State.Fields.Owner = known(7)
		expectFinding(t, OwnerExists(in), CheckOwnerExists, true, "unreadable")
	})
	t.Run("commitments that do not decode", func(t *testing.T) {
		in := baseInput()
		in.State.Fields.CurrentCommitments = known("not a list")
		expectFinding(t, CommitmentNotContradicted(in), CheckCommitmentHonored, true, "unreadable")
	})
	t.Run("next meeting of an unknown shape", func(t *testing.T) {
		for _, v := range []any{42, map[string]any{"when": "soon"}, map[string]any{"start": "not a time"}} {
			in := baseInput()
			in.State.Fields.NextMeeting = known(v)
			expectFinding(t, DatesMatchState(in), CheckDatesMatchState, true, "unreadable")
		}
	})
	t.Run("an unencodable input still gets an id", func(t *testing.T) {
		in := baseInput()
		in.State.Fields.Summary = known(make(chan int))
		if id := Results(Evaluate(in))[0].ID; len(id) != 36 {
			t.Fatalf("id = %q", id)
		}
	})
}
