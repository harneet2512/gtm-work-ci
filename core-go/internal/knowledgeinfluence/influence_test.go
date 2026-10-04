package knowledgeinfluence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type goldCase struct {
	ID    string `json:"id"`
	Eval  string `json:"eval"`
	Want  string `json:"want"`
	Why   string `json:"why"`
	Trace Trace  `json:"trace"`
}

func loadGold(t *testing.T) []goldCase {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "fixtures", "orchestrator", "e7_gold.json"))
		if err == nil {
			var doc struct {
				Cases []goldCase `json:"cases"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			return doc.Cases
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("fixtures/orchestrator/e7_gold.json not found")
		}
		dir = filepath.Dir(dir)
	}
}

func verdictOf(t *testing.T, eval string, tr Trace) Result {
	t.Helper()
	for _, r := range Grade(tr) {
		if r.Eval == eval {
			return r
		}
	}
	t.Fatalf("Grade returned no %s", eval)
	return Result{}
}

// TestGoldCasesAgree is the agreement check of the five E7 graders against their gold: every case, every eval.
func TestGoldCasesAgree(t *testing.T) {
	cases := loadGold(t)
	perEval := map[string]map[string]int{}
	agreed := 0
	for _, c := range cases {
		got := verdictOf(t, c.Eval, c.Trace)
		if got.Verdict == c.Want {
			agreed++
		} else {
			t.Errorf("%s: %s = %s (%s), gold says %s (%s)", c.ID, c.Eval, got.Verdict, got.Reason, c.Want, c.Why)
		}
		if perEval[c.Eval] == nil {
			perEval[c.Eval] = map[string]int{}
		}
		perEval[c.Eval][c.Want]++
		if got.Reason == "" {
			t.Errorf("%s: a verdict needs a reason", c.ID)
		}
	}
	for _, eval := range []string{"E7.1", "E7.2", "E7.3", "E7.4", "E7.5"} {
		n := 0
		for _, k := range perEval[eval] {
			n += k
		}
		if n < 5 {
			t.Errorf("%s has %d gold cases, want at least 5", eval, n)
		}
		if perEval[eval]["pass"] == 0 || (perEval[eval]["fail"] == 0 && perEval[eval]["warn"] == 0) {
			t.Errorf("%s gold needs both a passing and a failing case: %v", eval, perEval[eval])
		}
	}
	t.Logf("agreement %d/%d", agreed, len(cases))
}

func TestGradeReturnsTheFiveEvalsInOrder(t *testing.T) {
	got := Grade(Trace{})
	want := []string{"E7.1", "E7.2", "E7.3", "E7.4", "E7.5"}
	if len(got) != len(want) {
		t.Fatalf("got %d results", len(got))
	}
	for i, r := range got {
		if r.Eval != want[i] || r.Verdict == "" {
			t.Errorf("result %d = %+v", i, r)
		}
	}
}

func arm(strategy string, rank int, class, action string, people ...string) Arm {
	return Arm{StrategyType: strategy, Ranking: rank, ActionClass: class, ActionType: action, People: people}
}

func TestCompareMeasuresWhatKnowledgeChanged(t *testing.T) {
	without := []Arm{arm("wait", 1, "WAIT", "wait"), arm("reply", 2, "REPLY", "send_email", "p1"), arm("meet", 3, "MEETING", "schedule_meeting", "p1", "p2")}
	with := []Arm{
		arm("reply", 1, "REPLY", "send_email", "p1"),            // moved from 2 to 1
		arm("wait", 2, "WAIT", "wait"),                          // moved from 1 to 2
		arm("brief", 3, "ASK_RESEARCH", "internal_note", "rep"), // new with knowledge
	}
	changes, sum := Compare(with, without)
	if sum.Status != Compared || !sum.RankingChanged || !sum.PreferredChanged {
		t.Fatalf("summary = %+v", sum)
	}
	if c := changes[0]; !c.Matched || !c.RankingChanged || c.ActionChanged || c.RecipientsChanged || !c.Changed {
		t.Errorf("reply = %+v", c)
	}
	if c := changes[2]; c.Matched || !c.Changed {
		t.Errorf("a strategy that exists only with knowledge counts as changed: %+v", c)
	}
}

func TestCompareSeesActionAndRecipientChangesWithTheSameRank(t *testing.T) {
	without := []Arm{arm("a", 1, "REPLY", "send_email", "p1", "p2")}
	for name, with := range map[string]Arm{
		"action class": arm("a", 1, "MEETING", "schedule_meeting", "p1", "p2"),
		"recipients":   arm("a", 1, "REPLY", "send_email", "p1", "p3"),
		"fewer people": arm("a", 1, "REPLY", "send_email", "p1"),
	} {
		changes, _ := Compare([]Arm{with}, without)
		if !changes[0].Changed {
			t.Errorf("%s change not seen: %+v", name, changes[0])
		}
	}
	same, sum := Compare([]Arm{arm("a", 1, "REPLY", "send_email", "p2", "p1", "p1")}, without)
	if same[0].Changed || sum.RankingChanged || sum.PreferredChanged {
		t.Errorf("the same candidate (recipient order and duplicates aside) did not change: %+v %+v", same[0], sum)
	}
}

func TestCompareWithoutAnArmAHasNoChangeColumn(t *testing.T) {
	changes, sum := Compare([]Arm{arm("a", 1, "REPLY", "send_email")}, nil)
	if changes[0] != nil || sum.Status != NotRun {
		t.Errorf("got %+v %+v", changes[0], sum)
	}
	if changes, sum := Compare(nil, []Arm{arm("a", 1, "REPLY", "send_email")}); len(changes) != 0 || sum.Status != NotRun {
		t.Errorf("an empty arm B has nothing to compare: %+v", sum)
	}
}
