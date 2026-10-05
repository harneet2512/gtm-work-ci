package signalbench_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/signalbench"
)

const reportPath = "bench/reports/wp8-signals-triggers.json"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "fixtures", "gold")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("repository root not found")
		}
		dir = filepath.Dir(dir)
	}
}

// TestGoldReport scores the WP8 rules on the gold checkpoints and pins the committed report.
// Regenerate with: UPDATE_WP8_BENCH=1 go test ./internal/signalbench/ (from core-go).
func TestGoldReport(t *testing.T) {
	root := repoRoot(t)
	report, err := signalbench.Run(root)
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
	path := filepath.Join(root, filepath.FromSlash(reportPath))
	if os.Getenv("UPDATE_WP8_BENCH") == "1" {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (generate with UPDATE_WP8_BENCH=1)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), buf.Bytes()) {
		t.Fatalf("%s is stale; rerun with UPDATE_WP8_BENCH=1 and review the diff", reportPath)
	}
	if report.Checkpoints != 12 {
		t.Fatalf("checkpoints = %d", report.Checkpoints)
	}
	t.Logf("signals P=%v R=%v | material fields P=%v R=%v | trigger eligible=%v exact=%v",
		*report.Signals.Precision, *report.Signals.Recall, *report.MaterialFields.Precision, *report.MaterialFields.Recall,
		*report.Trigger.EligibleAccuracy, *report.Trigger.ExactReasonAccuracy)
}
