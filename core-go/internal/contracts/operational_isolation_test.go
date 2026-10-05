package contracts

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// HAR-140 / WP36 (HAR-97 canonical sections 2, 8, 12): token, latency, cost and tool-count metrics MUST NOT mutate GTM
// company knowledge. This scans every knowledge code path in core-go (the knowledge packages and every non-test file
// importing them) for operational quantities. The Python twin is worker-py/tests/test_operational_isolation.py.

// Optional underscores so snake_case and Go CamelCase field names (InputTokens, ModelCalls, DurationMs) both match.
var operationalWords = regexp.MustCompile(`(?i)(input_?tokens|output_?tokens|prompt_?tokens|completion_?tokens|cached_?tokens|reasoning_?tokens|total_?tokens|` +
	`token_?count|tokens_?used|latency|cost|tool_?calls?|model_?calls?|retry_?count|retry_?rate|duration_?ms|elapsed|(?:^|[^a-z])usd(?:[^a-z]|$))`)

var knowledgeDirs = []string{"knowledge", "knowledgestore", "knowledgebench"}

func operationalHits(text string) []string {
	seen := map[string]bool{}
	for _, m := range operationalWords.FindAllString(text, -1) {
		seen[strings.ToLower(m)] = true
	}
	hits := make([]string, 0, len(seen))
	for h := range seen {
		hits = append(hits, h)
	}
	sort.Strings(hits)
	return hits
}

func knowledgeGoSources(t *testing.T) []string {
	t.Helper()
	coreGo := filepath.Join(filepath.Dir(contractsDir(t)), "core-go")
	internal := filepath.Join(coreGo, "internal")
	var files []string
	err := filepath.WalkDir(coreGo, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(internal, path)
		inKnowledge := false
		for _, dir := range knowledgeDirs {
			if strings.HasPrefix(filepath.ToSlash(rel), dir+"/") {
				inKnowledge = true
			}
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if inKnowledge || strings.Contains(string(raw), "/internal/knowledge") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestOperationalScannerCatchesAMetricFeedingKnowledge(t *testing.T) {
	for _, probe := range []string{
		"conf := strengthen(k, run.input_tokens, latencyMs)",
		"conf := Strengthen(k, run.InputTokens, cfg.ModelCalls, d.DurationMs)",
		"c.RetryCount += r.TotalTokens + r.ToolCalls",
	} {
		if got := operationalHits(probe); len(got) < 2 {
			t.Fatalf("scanner missed operational metrics in %q: %v", probe, got)
		}
	}
	if got := operationalHits("applicability precision and scope"); len(got) != 0 {
		t.Fatalf("scanner flagged innocent text: %v", got)
	}
}

func TestNoKnowledgeCodePathReadsTokensLatencyCostOrToolCounts(t *testing.T) {
	files := knowledgeGoSources(t)
	if len(files) < 15 {
		t.Fatalf("the scan found only %d knowledge source files; it must cover the knowledge packages", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if hits := operationalHits(string(raw)); len(hits) != 0 {
			t.Errorf("%s: operational metric feeding a knowledge path: %v", f, hits)
		}
	}
}

func TestOperationalDiagnosticsNeverAllowKnowledgeMutation(t *testing.T) {
	var registry struct {
		Ops []struct {
			ID        string   `json:"id"`
			Class     string   `json:"class"`
			Mutation  bool     `json:"knowledge_mutation_allowed"`
			Consumers []string `json:"consumers"`
		} `json:"operational_diagnostics"`
	}
	decodeFile(t, filepath.Join(contractsDir(t), "metrics", "metric_registry.json"), &registry)
	if len(registry.Ops) != 5 {
		t.Fatalf("want the 5 operational diagnostics M1-M5, got %d", len(registry.Ops))
	}
	for _, o := range registry.Ops {
		if o.Class != "operational" || o.Mutation {
			t.Errorf("%s must be class operational and never allow knowledge mutation", o.ID)
		}
		for _, c := range o.Consumers {
			if c != "report" && c != "dashboard" && c != "budget_alert" {
				t.Errorf("%s feeds %q: only report, dashboard and budget_alert are allowed", o.ID, c)
			}
		}
	}
	var evals struct {
		Evals []struct {
			ID       string `json:"id"`
			Class    string `json:"class"`
			Mutation bool   `json:"knowledge_mutation_allowed"`
		} `json:"evals"`
	}
	decodeFile(t, filepath.Join(contractsDir(t), "evals", "eval_registry.json"), &evals)
	for _, e := range evals.Evals {
		if e.Class == "operational" && e.Mutation {
			t.Errorf("operational eval %s allows knowledge mutation", e.ID)
		}
	}
}
