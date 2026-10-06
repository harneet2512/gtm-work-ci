package bucket1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunGradesAllNineGatesInOrderAndNeverPassesWithoutMeasuring(t *testing.T) {
	rs := Run(goodEpisode(), nil)
	if len(rs) != 9 {
		t.Fatalf("%d results", len(rs))
	}
	for i, r := range rs {
		if r.Gate != GateOrder[i] || r.Calibrated || r.Question == "" || r.Improves == "" || r.SpanID == "" || r.JudgedObject.ID == "" {
			t.Errorf("%s shape: %+v", GateOrder[i], r)
		}
		if r.Verdict == Pass && len(r.EvidenceRefs) == 0 {
			t.Errorf("%s passed with no evidence", r.Gate)
		}
	}
	// goodEpisode has no precedents, attribution, beliefs or trace: those gates are not measured.
	for _, g := range []int{4, 5, 6, 7, 8} {
		if rs[g].Verdict != Unknown || rs[g].Observed == "" {
			t.Errorf("%s = %s, want unknown (not measured)", rs[g].Gate, rs[g].Verdict)
		}
	}
	// The model-judged gates are unknown too until a judgment is recorded.
	if rs[0].Verdict != Unknown {
		t.Errorf("B1 = %s without a recorded judgment", rs[0].Verdict)
	}
}

func TestNormalizeVerdictTreatsAbstainAsUnknownAndNeverAPass(t *testing.T) {
	for in, want := range map[string]string{"pass": Pass, "PASS": Pass, "warn": Warn, "fail": Fail, "unknown": Unknown, "abstain": Unknown, "": Unknown, "passed!": Unknown} {
		if got := NormalizeVerdict(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestAPassWithoutEvidenceBecomesUnknown(t *testing.T) {
	r := rollup(goodEpisode(), "B2", JudgedObject{Type: "Episode", ID: "x"}, []Assertion{{Name: "a", Verdict: "pass", Grader: GraderDeterministic}})
	if r.Verdict != Unknown {
		t.Fatalf("%s", r.Verdict)
	}
	r = rollup(goodEpisode(), "B2", JudgedObject{Type: "Episode", ID: "x"}, []Assertion{{Name: "a", Verdict: "abstain"}})
	if r.Verdict != Unknown {
		t.Fatalf("abstain read as %s", r.Verdict)
	}
	if r = rollup(goodEpisode(), "B2", JudgedObject{}, nil); r.Verdict != Unknown {
		t.Fatalf("no assertions = %s", r.Verdict)
	}
}

func TestJudgmentsAreReplayedFromACassetteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "judgments.json")
	if js, err := LoadJudgments(path, "ep-13"); err != nil || js != nil {
		t.Fatalf("a missing file is nothing recorded: %v %v", js, err)
	}
	raw, _ := json.Marshal(map[string]map[string]Judgment{"ep-13": {"B1": {Gate: "B1", Model: "qwen/qwen3.8-flash", Assertions: []Assertion{
		{Name: "inference_boundary", Verdict: "pass", Why: "labelled", Refs: []Ref{{ActivityID: "a1", ClaimID: "c3"}}}}}}})
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	js, err := LoadJudgments(path, "ep-13")
	if err != nil || js["B1"].Model != "qwen/qwen3.8-flash" {
		t.Fatalf("%v %v", js, err)
	}
	if r := GradeB1(goodEpisode(), js); r.Verdict != Pass || r.Grader != GraderMixed {
		t.Fatalf("%s %s", r.Verdict, r.Why)
	}
	if err := os.WriteFile(path, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadJudgments(path, "ep-13"); err == nil {
		t.Fatal("a malformed cassette must be an explicit error")
	}
}

func TestResultsRoundTripThroughTheArtifactFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	if err := WriteResults(path, "ep-13", Run(goodEpisode(), nil)); err != nil {
		t.Fatal(err)
	}
	if err := WriteResults(path, "ep-eco", Run(goodEpisode(), nil)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var f File
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Episodes) != 2 || len(f.Episodes["ep-13"]) != 9 {
		t.Fatalf("%v %+v", err, f)
	}
	if err := os.WriteFile(path, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteResults(path, "x", nil); err == nil {
		t.Fatal("a corrupt artifact must not be overwritten silently")
	}
}

func TestReadEpisodeNeedsAnIDAndAWorldTime(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "g.json")
	raw, _ := json.Marshal(goodEpisode())
	if err := os.WriteFile(good, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if ep, err := ReadEpisode(good); err != nil || ep.ID != "ep-13" {
		t.Fatalf("%v", err)
	}
	bad := filepath.Join(dir, "b.json")
	_ = os.WriteFile(bad, []byte(`{"id":"x"}`), 0o644)
	if _, err := ReadEpisode(bad); err == nil {
		t.Fatal("no world time must be refused")
	}
	if _, err := ReadEpisode(filepath.Join(dir, "none.json")); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestGateTableMatchesTheRegistry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "evals", "eval_registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Gates []struct{ ID, Question, Improves string } `json:"gates"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, g := range reg.Gates {
		info, ok := Gates[g.ID]
		if !ok {
			continue
		}
		seen++
		if info.Question != g.Question {
			t.Errorf("%s question differs:\n%q\n%q", g.ID, info.Question, g.Question)
		}
		if info.Improves != g.Improves {
			t.Errorf("%s improves differs:\n%q\n%q", g.ID, info.Improves, g.Improves)
		}
	}
	if seen != 9 {
		t.Fatalf("%d of 9 Bucket 1 gates are in the registry", seen)
	}
}
