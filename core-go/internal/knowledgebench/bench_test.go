package knowledgebench

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

const reportPath = "bench/reports/knowledge-applicability-legacy.json"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "fixtures", "evals", "cases")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("repository root not found")
		}
		dir = filepath.Dir(dir)
	}
}

// TestRetiredFixtureReport runs the matcher over the retired fixture cases (invented accounts) in both signal modes and
// pins the committed report. It is a matcher regression, not a measurement: the cases are retired as gold
// (fixtures/evals/cases/RETIRED.json) and Run reports no gold for them. Regenerate with UPDATE_KNOWLEDGE_BENCH=1 go test
// ./internal/knowledgebench/.
func TestRetiredFixtureReport(t *testing.T) {
	root := repoRoot(t)
	report, err := RunRetiredFixture(root)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	path := filepath.Join(root, filepath.FromSlash(reportPath))
	if os.Getenv("UPDATE_KNOWLEDGE_BENCH") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (generate with UPDATE_KNOWLEDGE_BENCH=1)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s is stale; rerun with UPDATE_KNOWLEDGE_BENCH=1 and review the diff", reportPath)
	}
	if len(report.Modes) != 3 || report.Modes[0].Mode != ModeCorrectUpstream || report.Modes[1].Mode != ModeOwnDiff || report.Modes[2].Mode != ModeWP8InMemoryHistory {
		t.Fatalf("want the three signal modes side by side, got %d", len(report.Modes))
	}
	for _, m := range report.Modes {
		t.Logf("%s: pairs=%d accuracy=%s precision=%s recall=%s exception_recall=%s invalid_condition_misses=%d", m.Mode,
			m.Metrics.Pairs, f(m.Metrics.Accuracy), f(m.Metrics.ApplicabilityPrecision), f(m.Metrics.ApplicabilityRecall),
			f(m.Metrics.ExceptionRecall), m.Metrics.MissesInvalidCondition)
	}
}

func f(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*v, 'f', 3, 64)
}

// The K21 and K05 misses are reported as invalid conditions, not silent matcher misses.
func TestKnownPoorlyWrittenExceptionsAreInvalidConditions(t *testing.T) {
	report, err := RunRetiredFixture(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	upstream := report.Modes[0]
	causes := map[string]string{}
	for _, r := range upstream.Rows {
		if !r.Correct {
			causes[r.CaseID] = r.MissCause
		}
	}
	want := map[string]string{
		"acme_cp4_k21_budget_already_approved_trap":     "invalid_condition",
		"beta_silence_k05_trap_rep_owes_residency_docs": "invalid_condition",
	}
	if len(causes) != len(want) {
		t.Fatalf("misses = %v", causes)
	}
	for id, cause := range want {
		if causes[id] != cause {
			t.Fatalf("misses = %v", causes)
		}
	}
	if upstream.Metrics.MissesInvalidCondition != 2 {
		t.Fatalf("metrics = %+v", upstream.Metrics)
	}
}

func TestUnknownModeIsAnError(t *testing.T) {
	if _, err := LoadLegacyPairs(repoRoot(t), "telepathy"); err == nil {
		t.Fatal("unknown mode must be an error")
	}
}

// Every offered knowledge object in a case with a knowledge judgment is labelled exactly once.
func TestLegacyPairsCoverEveryLabelledKnowledge(t *testing.T) {
	pairs, err := LoadLegacyPairs(repoRoot(t), ModeCorrectUpstream)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	labels := map[string]int{}
	for _, p := range pairs {
		key := p.CaseID + "/" + p.Knowledge.ID
		if seen[key] {
			t.Fatalf("pair %s labelled twice", key)
		}
		seen[key] = true
		labels[p.Gold]++
	}
	if len(pairs) < 15 || labels[knowledge.LabelApplies] == 0 || labels[knowledge.LabelDoesNotApply] == 0 || labels[knowledge.LabelExceptionTriggered] == 0 {
		t.Fatalf("legacy gold lost a label class: %v", labels)
	}
}

// HAR-106: persisting signals with occurred_at and a window fixes what own-diff signals miss. Exception
// recall was 0.000 with each diff's own signals only (new_stakeholder_entered fired in an EARLIER diff).
func TestPersistedSignalHistoryRecoversTheEarlierDiffSignals(t *testing.T) {
	report, err := RunRetiredFixture(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	oracle, own, persisted := report.Modes[0].Metrics, report.Modes[1].Metrics, report.Modes[2].Metrics
	if own.ExceptionRecall == nil || *own.ExceptionRecall != 0 {
		t.Fatalf("own-diff exception recall = %v, the baseline this work fixes", own.ExceptionRecall)
	}
	if persisted.ExceptionRecall == nil || *persisted.ExceptionRecall < 0.7 {
		t.Fatalf("persisted-history exception recall = %v", persisted.ExceptionRecall)
	}
	if *persisted.Accuracy < *own.Accuracy || *persisted.ApplicabilityRecall < *own.ApplicabilityRecall {
		t.Fatalf("persisted history must not be worse than own-diff: %+v vs %+v", persisted, own)
	}
	if *persisted.Accuracy < *oracle.Accuracy-0.001 {
		t.Fatalf("rule-derived history should match the gold-label oracle: %v vs %v", *persisted.Accuracy, *oracle.Accuracy)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := LoadLegacyPairs(t.TempDir(), ModeOwnDiff); err == nil {
		t.Fatal("an empty root must be an error")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "x", "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	var v any
	if readJSON(bad, &v) == nil || readJSON(filepath.Join(dir, "missing.json"), &v) == nil {
		t.Fatal("unreadable JSON must be an error")
	}
}

// While fixtures/evals/cases is retired the benchmark says there is no gold: no modes, no metrics, no numbers a reader
// could take for a measurement on real accounts.
func TestRunReportsNoGoldWhileTheLegacyFixtureIsRetired(t *testing.T) {
	report, err := Run(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if !report.NoGold || len(report.Modes) != 0 {
		t.Fatalf("report = %+v, want no gold and no modes", report)
	}
	if !strings.HasPrefix(report.GoldSource, "no gold: ") || !strings.Contains(report.GoldSource, "retired") {
		t.Fatalf("gold source = %q", report.GoldSource)
	}
}

func TestRunScoresALiveFixtureDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "fixtures", "evals", "cases"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No marker: not retired, so Run goes on to load cases (and fails here, because the tree has none), never "no gold".
	if _, err := Run(root); err == nil {
		t.Fatal("an empty live gold directory must be an error, not a silent no-gold report")
	}
}
