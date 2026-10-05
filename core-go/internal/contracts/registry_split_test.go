package contracts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// HAR-140 / WP36: the split registries (HAR-97 canonical section 12). Conformance of the four instances against their
// schemas, plus the cross-references the schemas cannot express. The Python twins are worker-py/tests/test_eval_registry.py,
// test_completeness_matrix.py, test_trace_schema.py and test_metric_registry_split.py.

type evalEntry struct {
	ID         string   `json:"id"`
	Class      string   `json:"class"`
	Status     string   `json:"status"`
	Surfaces   []int    `json:"surfaces"`
	Dimensions []string `json:"dimensions"`
}

type matrixCell struct {
	Surface   int      `json:"surface"`
	Dimension string   `json:"dimension"`
	State     string   `json:"state"`
	Evals     []string `json:"evals"`
	Owner     string   `json:"owner"`
	Status    string   `json:"status"`
	Reason    string   `json:"reason"`
}

func decodeFile(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", filepath.Base(path), err)
	}
}

func TestSplitRegistryInstancesValidate(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	for file, schema := range map[string]string{
		"metrics/metric_registry.json":   "metric_registry",
		"evals/eval_registry.json":       "eval_registry",
		"evals/completeness_matrix.json": "completeness_matrix",
		"traces/trace_schema.json":       "trace_schema",
	} {
		if err := schemaFor(t, c, schema).Validate(readJSON(t, filepath.Join(root, file))); err != nil {
			t.Errorf("%s invalid: %v", file, err)
		}
	}
}

func TestSplitRegistryCountsAndMatrixReferences(t *testing.T) {
	root := contractsDir(t)
	var reg struct {
		Families []struct{ ID string } `json:"families"`
		Evals    []evalEntry           `json:"evals"`
	}
	var matrix struct {
		Cells []matrixCell `json:"cells"`
	}
	decodeFile(t, filepath.Join(root, "evals", "eval_registry.json"), &reg)
	decodeFile(t, filepath.Join(root, "evals", "completeness_matrix.json"), &matrix)
	if len(reg.Families) != 27 || len(reg.Evals) != 213 {
		t.Fatalf("registry has %d families and %d evals, want 27 and 213 (E1-E22, M1-M5)", len(reg.Families), len(reg.Evals))
	}
	byID := map[string]evalEntry{}
	for _, e := range reg.Evals {
		if _, dup := byID[e.ID]; dup {
			t.Errorf("duplicate eval id %s", e.ID)
		}
		byID[e.ID] = e
	}
	if len(matrix.Cells) != 200 {
		t.Fatalf("matrix has %d cells, want 20 x 10", len(matrix.Cells))
	}
	seen := map[[2]any]bool{}
	for _, cell := range matrix.Cells {
		seen[[2]any{cell.Surface, cell.Dimension}] = true
		for _, p := range cellProblems(cell, byID) {
			t.Error(p)
		}
	}
	if len(seen) != 200 {
		t.Errorf("cells are not unique per surface x dimension: %d distinct", len(seen))
	}
}

// cellProblems is the Go twin of the construction gate: a REQUIRED cell must be planned with an owner, a covered cell
// must name existing evals that protect it, a not-applicable cell must say why.
func cellProblems(cell matrixCell, byID map[string]evalEntry) []string {
	where := fmt.Sprintf("surface %d %s", cell.Surface, cell.Dimension)
	var problems []string
	switch cell.State {
	case "COVERED_BY":
		if len(cell.Evals) == 0 {
			problems = append(problems, where+": covered by nothing")
		}
		for _, id := range cell.Evals {
			e, ok := byID[id]
			if !ok || e.Status == "planned" {
				problems = append(problems, fmt.Sprintf("%s: %s is not an existing registered eval", where, id))
			}
			if !contains(e.Surfaces, cell.Surface) || !containsStr(e.Dimensions, cell.Dimension) {
				problems = append(problems, fmt.Sprintf("%s: %s does not protect this cell", where, id))
			}
		}
	case "REQUIRED":
		if cell.Status != "planned" || !strings.HasPrefix(cell.Owner, "HAR-") || len(cell.Evals) == 0 {
			problems = append(problems, where+": REQUIRED with no eval registered and not planned with an owning Linear issue and a registered planned eval")
		}
	case "NOT_APPLICABLE":
		if len(cell.Reason) < 20 {
			problems = append(problems, where+": not applicable needs a reason")
		}
	default:
		problems = append(problems, fmt.Sprintf("%s: unknown state %q", where, cell.State))
	}
	return problems
}

func TestConstructionGateRejectsAnUnplannedRequiredCell(t *testing.T) {
	byID := map[string]evalEntry{"E1.1": {ID: "E1.1", Status: "implemented", Surfaces: []int{1}, Dimensions: []string{"INTENDED_BEHAVIOR"}}}
	bad := []matrixCell{
		{Surface: 1, Dimension: "BOUNDARIES", State: "REQUIRED"},
		{Surface: 1, Dimension: "BOUNDARIES", State: "REQUIRED", Status: "planned", Owner: "WP36"},
		{Surface: 1, Dimension: "BOUNDARIES", State: "REQUIRED", Status: "planned", Owner: "HAR-121"},
		{Surface: 1, Dimension: "BOUNDARIES", State: "NOT_APPLICABLE", Reason: "n/a"},
		{Surface: 1, Dimension: "BOUNDARIES", State: "COVERED_BY", Evals: []string{"E1.1"}},
		{Surface: 1, Dimension: "INTENDED_BEHAVIOR", State: "COVERED_BY", Evals: []string{"E9.9"}},
	}
	for _, cell := range bad {
		if len(cellProblems(cell, byID)) == 0 {
			t.Errorf("the gate accepted %+v", cell)
		}
	}
	good := matrixCell{Surface: 1, Dimension: "INTENDED_BEHAVIOR", State: "COVERED_BY", Evals: []string{"E1.1"}}
	if p := cellProblems(good, byID); len(p) != 0 {
		t.Errorf("the gate rejected a covered cell: %v", p)
	}
}

func TestLegacyMetricIDsAllMapToANewEntry(t *testing.T) {
	root := contractsDir(t)
	var registry struct {
		Metrics []struct{ ID, Kind, Status string } `json:"metrics"`
	}
	var legacy struct {
		Map []struct{ Old, New, Status string } `json:"map"`
	}
	decodeFile(t, filepath.Join(root, "metrics", "metric_registry.json"), &registry)
	decodeFile(t, filepath.Join(root, "metrics", "legacy_id_map.json"), &legacy)
	ids := map[string]bool{}
	for _, m := range registry.Metrics {
		ids[m.ID] = true
	}
	if len(legacy.Map) != 268 {
		t.Fatalf("legacy map has %d entries, want 268", len(legacy.Map))
	}
	for _, r := range legacy.Map {
		if !ids[r.New] {
			t.Errorf("legacy id %s maps to %s which is not in metric_registry.json", r.Old, r.New)
		}
	}
	if len(registry.Metrics) != 278 {
		t.Errorf("registry has %d metrics, want 268 migrated + 10 canonical", len(registry.Metrics))
	}
}

func TestTraceSchemaSpansAreOrderedAndAcyclic(t *testing.T) {
	var trace struct {
		Spans []struct {
			ID      string   `json:"id"`
			Order   int      `json:"order"`
			Parents []string `json:"parents"`
		} `json:"spans"`
	}
	decodeFile(t, filepath.Join(contractsDir(t), "traces", "trace_schema.json"), &trace)
	if len(trace.Spans) != 22 {
		t.Fatalf("trace has %d spans, want the 22 links of HAR-97 section 3", len(trace.Spans))
	}
	order := map[string]int{}
	for i, s := range trace.Spans {
		if s.Order != i+1 {
			t.Errorf("span %s has order %d, want %d", s.ID, s.Order, i+1)
		}
		order[s.ID] = s.Order
	}
	for _, s := range trace.Spans {
		for _, p := range s.Parents {
			if order[p] == 0 || order[p] >= s.Order {
				t.Errorf("span %s derives from %s which is not an earlier span", s.ID, p)
			}
		}
	}
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
