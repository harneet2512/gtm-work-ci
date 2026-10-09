package evalarea_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalarea"
)

func contractsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "contracts")
		if info, statErr := os.Stat(filepath.Join(candidate, "evals")); statErr == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("contracts/ not found")
		}
		dir = parent
	}
}

func decode(t *testing.T, rel string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(contractsDir(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", rel, err)
	}
}

type areasFile struct {
	Areas []struct {
		ID       string   `json:"id"`
		Label    string   `json:"label"`
		Order    int      `json:"order"`
		Families []string `json:"families"`
	} `json:"areas"`
	MetricFamilies []string          `json:"metric_families"`
	FamilySpans    map[string]string `json:"family_spans"`
	EvalTypes      map[string]string `json:"eval_types"`
}

type registryEval struct {
	ID            string `json:"id"`
	Family        string `json:"family"`
	Area          string `json:"area"`
	DefinitionRef struct {
		CatalogTypes []string `json:"catalog_types"`
	} `json:"definitions_ref"`
}

type registryFile struct {
	Families []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"families"`
	Evals   []registryEval `json:"evals"`
	Metrics []registryEval `json:"metrics"`
}

// TestGoTableMirrorsTheContractFile is the JSON-to-Go parity test: contracts/evals/eval_areas.json is the source of
// truth and the compiled table must equal it, area by area and type by type.
func TestGoTableMirrorsTheContractFile(t *testing.T) {
	var file areasFile
	decode(t, "evals/eval_areas.json", &file)

	got := evalarea.Areas()
	if len(got) != len(file.Areas) {
		t.Fatalf("areas: Go has %d, contract has %d", len(got), len(file.Areas))
	}
	for i, want := range file.Areas {
		a := got[i]
		if string(a.ID) != want.ID || a.Label != want.Label || a.Order != want.Order || !slices.Equal(a.Families, want.Families) {
			t.Errorf("area %d: Go %+v, contract %+v", i, a, want)
		}
	}
	for evalType, family := range file.EvalTypes {
		if f, ok := evalarea.FamilyOf(evalType); !ok || f != family {
			t.Errorf("FamilyOf(%q) = %q, %v; contract says %q", evalType, f, ok, family)
		}
	}
	if n := len(evalarea.EvalTypes()); n != len(file.EvalTypes) {
		t.Errorf("Go maps %d eval types, contract maps %d", n, len(file.EvalTypes))
	}
	for family, span := range file.FamilySpans {
		if s, ok := evalarea.SpanOfFamily(family); !ok || s != span {
			t.Errorf("SpanOfFamily(%q) = %q, %v; contract says %q", family, s, ok, span)
		}
	}
	for _, a := range evalarea.Areas() {
		for _, family := range a.Families {
			if _, inFile := file.FamilySpans[family]; !inFile {
				if s, ok := evalarea.SpanOfFamily(family); ok {
					t.Errorf("Go gives %s the span %q; the contract gives it none", family, s)
				}
			}
		}
	}
}

func TestSpanOfFamilyResolvesAndRefusesMetricsAndUnknowns(t *testing.T) {
	if s, ok := evalarea.SpanOfEvalType("knowledge_applicability"); !ok || s != "knowledge_applicable" {
		t.Errorf("SpanOfEvalType = %q, %v", s, ok)
	}
	if s, ok := evalarea.SpanOfEvalType("trajectory"); ok {
		t.Errorf("trajectory is a trace-integrity eval (E18): no span, got %q", s)
	}
	for _, name := range []string{"M1", "E999", ""} {
		if s, ok := evalarea.SpanOfFamily(name); ok {
			t.Errorf("SpanOfFamily(%q) = %q", name, s)
		}
	}
	if _, ok := evalarea.SpanOfEvalType("not_an_eval"); ok {
		t.Error("an unknown eval type has no span")
	}
}

// TestEveryFamilyIsInTheRegistryAndMetricsAreNeverAnArea pins the cross-references the JSON Schema cannot express.
func TestEveryFamilyIsInTheRegistryAndMetricsAreNeverAnArea(t *testing.T) {
	var file areasFile
	var reg registryFile
	decode(t, "evals/eval_areas.json", &file)
	decode(t, "evals/eval_registry.json", &reg)

	names := map[string]string{}
	for _, f := range reg.Families {
		names[f.ID] = f.Name
	}
	areaOf := map[string]string{}
	for _, a := range file.Areas {
		for _, fam := range a.Families {
			if _, ok := names[fam]; !ok {
				t.Errorf("area %s lists family %s, which is not in the registry", a.ID, fam)
			}
			if prev, dup := areaOf[fam]; dup {
				t.Errorf("family %s is in both %s and %s", fam, prev, a.ID)
			}
			areaOf[fam] = a.ID
		}
	}
	for _, f := range reg.Families {
		isMetric := slices.Contains(file.MetricFamilies, f.ID)
		_, inArea := areaOf[f.ID]
		if isMetric == inArea {
			t.Errorf("family %s must be in exactly one of an area or metric_families (metric=%v area=%v)", f.ID, isMetric, inArea)
		}
		if got, ok := evalarea.FamilyName(f.ID); !isMetric && (!ok || got != f.Name) {
			t.Errorf("FamilyName(%s) = %q, %v; registry says %q", f.ID, got, ok, f.Name)
		}
	}
}

// TestPrimaryFamilyIsOneOfTheRegistrysOwnLinks: where the registry links an eval type to families (definitions_ref
// catalog_types), the primary family chosen here is one of them; the registry does not contradict this file.
func TestPrimaryFamilyIsOneOfTheRegistrysOwnLinks(t *testing.T) {
	var file areasFile
	var reg registryFile
	decode(t, "evals/eval_areas.json", &file)
	decode(t, "evals/eval_registry.json", &reg)

	linked := map[string][]string{}
	for _, e := range reg.Evals {
		for _, ct := range e.DefinitionRef.CatalogTypes {
			if !slices.Contains(linked[ct], e.Family) {
				linked[ct] = append(linked[ct], e.Family)
			}
		}
	}
	for evalType, family := range file.EvalTypes {
		if links := linked[evalType]; len(links) > 0 && !slices.Contains(links, family) {
			t.Errorf("%s -> %s, but the registry links it only to %v", evalType, family, links)
		}
	}
}

func TestAreaOfResolvesThroughTheFamily(t *testing.T) {
	cases := map[string]evalarea.Area{
		"provenance_coverage":     evalarea.Intelligence,
		"champion_continuity":     evalarea.DecisionLearning,
		"cta_calibration":         evalarea.DecisionLearning, // E12: the action artifact is a stage of the loop (ACT)
		"crm_writeback":           evalarea.DecisionLearning, // E13: ACT (tools, permissions, writes) is Decision & Learning
		"duplicate_action":        evalarea.DecisionLearning, // E14: environment / workflow outcome of the action
		"trajectory":              evalarea.System,           // E18: trace integrity is operational
		"knowledge_applicability": evalarea.DecisionLearning,
	}
	for evalType, want := range cases {
		if got, ok := evalarea.AreaOf(evalType); !ok || got != want {
			t.Errorf("AreaOf(%q) = %q, %v; want %q", evalType, got, ok, want)
		}
	}
}

func TestSystemHoldsOnlyTheOperationalFamilies(t *testing.T) {
	var system []string
	for _, a := range evalarea.Areas() {
		if a.ID == evalarea.System {
			system = a.Families
		}
	}
	want := []string{"E18", "E19", "E20", "E21", "E22"}
	if !slices.Equal(system, want) {
		t.Fatalf("System families = %v, want %v: the ACT families E13/E14 belong to Decision & Learning (HAR-145)", system, want)
	}
	for _, f := range []string{"E13", "E14"} {
		if a, ok := evalarea.AreaOfFamily(f); !ok || a != evalarea.DecisionLearning {
			t.Errorf("%s -> %q, %v", f, a, ok)
		}
	}
}

func TestUnknownNamesAreNotInventedAnArea(t *testing.T) {
	for _, name := range []string{"", "not_an_eval", "M1", "E1"} {
		if a, ok := evalarea.AreaOf(name); ok {
			t.Errorf("AreaOf(%q) = %q, want no area", name, a)
		}
	}
	if _, ok := evalarea.FamilyOf("nope"); ok {
		t.Error("FamilyOf of an unknown type must report false")
	}
	if _, ok := evalarea.AreaOfFamily("M3"); ok {
		t.Error("a metric family belongs to no area")
	}
	if _, ok := evalarea.FamilyName("E999"); ok {
		t.Error("an unknown family has no name")
	}
}

func TestAreasReturnsACopy(t *testing.T) {
	a := evalarea.Areas()
	a[0].Families[0] = "tampered"
	a[0].Label = "tampered"
	if again := evalarea.Areas(); again[0].Families[0] == "tampered" || again[0].Label == "tampered" {
		t.Fatal("Areas must hand out copies: the table is immutable")
	}
	types := evalarea.EvalTypes()
	delete(types, "provenance_coverage")
	if _, ok := evalarea.FamilyOf("provenance_coverage"); !ok {
		t.Fatal("EvalTypes must hand out a copy")
	}
}

// TestFeedbackFamiliesAreDecisionLearningAndEveryEvalStageIsKnown: E10-E12 (interaction, propagation, action) are the
// loop's own stages, not Cliff / Experience (HAR-145); each registry eval's area is its family's, (the gate it sits under is the registry's own).
func TestFeedbackFamiliesAreDecisionLearningAndEveryEvalStageIsKnown(t *testing.T) {
	for _, f := range []string{"E10", "E11", "E12"} {
		if a, ok := evalarea.AreaOfFamily(f); !ok || a != evalarea.DecisionLearning {
			t.Errorf("%s -> %q, %v; want decision_learning", f, a, ok)
		}
	}
	for _, a := range evalarea.Areas() {
		if a.ID == evalarea.CliffExperience && len(a.Families) != 0 {
			t.Errorf("Cliff / Experience holds messages, not eval families: %v", a.Families)
		}
	}
	var reg registryFile
	decode(t, "evals/eval_registry.json", &reg)
	for _, e := range reg.Evals {
		if e.Area != "" {
			a, inArea := evalarea.AreaOfFamily(e.Family)
			if (inArea && string(a) != e.Area) || (!inArea && e.Area != "system") {
				t.Errorf("%s says area %q but its family %s is in %q", e.ID, e.Area, e.Family, a)
			}
		}
	}
	if len(reg.Metrics) != 5 {
		t.Errorf("registry holds %d metrics, want M1-M5", len(reg.Metrics))
	}
}

func TestSplitJudgesEachServeOneFamily(t *testing.T) {
	want := map[string]string{"decision_grounding": "E8", "artifact_grounding": "E12", "decision_timing": "E8", "artifact_timing": "E12"}
	for evalType, family := range want {
		if f, ok := evalarea.FamilyOf(evalType); !ok || f != family {
			t.Errorf("FamilyOf(%q) = %q, %v; want %q", evalType, f, ok, family)
		}
	}
}

func TestToolsExecutionAndKnowledgeUseHaveTheirOwnSpans(t *testing.T) {
	for family, want := range map[string]string{"E13": "tool_call", "E14": "execution", "E16": "knowledge_used"} {
		if s, ok := evalarea.SpanOfFamily(family); !ok || s != want {
			t.Errorf("SpanOfFamily(%s) = %q, %v; want %q", family, s, ok, want)
		}
	}
}
