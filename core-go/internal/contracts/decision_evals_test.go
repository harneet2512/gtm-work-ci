package contracts

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// WP-A (contracts-decision-evals): the registry maps every decision and feedback-loop eval (E8-E17) to what it judges,
// where, when and under which stage; EvalResult carries the object and the span and follows rule R1 (no evidence, no
// pass); DecisionRanking and KnowledgeUse are contracts of their own. Python twin: test_decision_eval_registry.py,
// which holds the audited per-eval table.

type mappedEval struct {
	ID           string `json:"id"`
	Family       string `json:"family"`
	Status       string `json:"status"`
	Job          string `json:"job"`
	Area         string `json:"area"`
	Bucket       string `json:"bucket"`
	Gate         string `json:"gate"`
	Question     string `json:"question"`
	Improves     string `json:"improves"`
	SpanKind     string `json:"span_kind"`
	JudgedObject struct {
		Type    string `json:"type"`
		IDField string `json:"id_field"`
	} `json:"judged_object"`
	DemoMoment []string `json:"demo_moment"`
}

func loadMapped(t *testing.T) (evals []mappedEval, metrics []mappedEval) {
	t.Helper()
	var reg struct {
		Evals   []mappedEval `json:"evals"`
		Metrics []mappedEval `json:"metrics"`
	}
	decodeFile(t, filepath.Join(contractsDir(t), "evals", "eval_registry.json"), &reg)
	return reg.Evals, reg.Metrics
}

func inLoop(family string) bool {
	return slices.Contains([]string{"E8", "E9", "E10", "E11", "E12", "E13", "E14", "E15", "E16", "E17"}, family)
}

func TestEveryLoopEvalCarriesItsMapping(t *testing.T) {
	evals, _ := loadMapped(t)
	gate := regexp.MustCompile(`^(B[1-9]|D([1-9]|10)|S[1-5])$`)
	moments := []string{"M2", "CES", "M3", "ECOLITE", "OFFLINE", "LATER"}
	seen := 0
	for _, e := range evals {
		if !inLoop(e.Family) || e.Status == "hidden" {
			continue
		}
		seen++
		if e.Job != "decision_loop" || e.Area != "decision_learning" || !gate.MatchString(e.Gate) && e.Bucket != "" && e.Question != "" && e.Improves != "" {
			t.Errorf("%s: job %q area %q gate %q", e.ID, e.Job, e.Area, e.Gate)
		}
		if e.SpanKind == "" || e.JudgedObject.Type == "" || e.JudgedObject.IDField == "" || len(e.DemoMoment) == 0 {
			t.Errorf("%s lacks its span, judged object or demo moment: %+v", e.ID, e)
		}
		for _, m := range e.DemoMoment {
			if !slices.Contains(moments, m) {
				t.Errorf("%s: unknown demo moment %q", e.ID, m)
			}
		}
	}
	if seen != 101 { // 13+8+8+9+10+14+9+13+7+10: E8-E17 less E11.9 (a metric) and E16.5 (hidden)
		t.Errorf("mapped %d loop evals, want 101", seen)
	}
}

func TestRankingRatesAreRankStageEvalsOnTheRankingSpan(t *testing.T) {
	evals, _ := loadMapped(t)
	for _, e := range evals {
		if e.Family == "E9" && (e.Gate != "D3" || e.SpanKind != "ranking" || e.JudgedObject.Type != "DecisionRanking") {
			t.Errorf("%s: %+v", e.ID, e)
		}
		if e.Family == "E13" && e.SpanKind != "tool_call" || e.Family == "E14" && e.SpanKind != "execution" {
			t.Errorf("%s attaches to %q", e.ID, e.SpanKind)
		}
	}
}

func TestMetricsAreNotCountedAndHiddenEvalsAreHidden(t *testing.T) {
	evals, metrics := loadMapped(t)
	if len(metrics) != 5 {
		t.Fatalf("want M1-M5, got %d", len(metrics))
	}
	var hidden []string
	for _, e := range evals {
		if strings.HasPrefix(e.ID, "M") || e.ID == "E11.9" {
			t.Errorf("%s must not be an eval", e.ID)
		}
		if e.Status == "hidden" {
			hidden = append(hidden, e.ID)
		}
	}
	slices.Sort(hidden)
	if !slices.Equal(hidden, []string{"E16.5", "E22.4"}) {
		t.Errorf("hidden evals = %v, want E16.5 and E22.4", hidden)
	}
}

func TestEvalResultRuleR1AndRequiredObject(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "eval_result")
	semanticPass := func(m map[string]any) {
		m["kind"], m["verdict"], m["blocking"], m["model"] = "semantic", "pass", false, "m"
		m["state_refs"], m["activity_refs"], m["evidence_refs"], m["knowledge_refs"] = []any{}, []any{}, []any{}, []any{}
	}
	bad := []struct {
		name string
		fn   func(m map[string]any)
	}{
		{"no judged object", func(m map[string]any) { delete(m, "judged_object") }},
		{"no span", func(m map[string]any) { delete(m, "span_id") }},
		{"a malformed span id", func(m map[string]any) { m["span_id"] = "Candidates" }},
		{"a judged object without an id", func(m map[string]any) { m["judged_object"] = map[string]any{"type": "StrategySet"} }},
		{"R1: a semantic pass that cites nothing", semanticPass},
	}
	for _, tc := range bad {
		if err := schema.Validate(mutate(t, root, "eval_result", tc.fn)); err == nil {
			t.Errorf("accepted: %s", tc.name)
		}
	}
	good := []struct {
		name string
		fn   func(m map[string]any)
	}{
		{"unknown when nothing is cited", func(m map[string]any) { semanticPass(m); m["verdict"] = "unknown" }},
		{"a pass that cites a state field", func(m map[string]any) { semanticPass(m); m["state_refs"] = []any{"champion"} }},
		{"a pass that cites knowledge", func(m map[string]any) {
			semanticPass(m)
			m["knowledge_refs"] = []any{"0c17c000-0000-4000-8000-000000000017"}
		}},
		{"a deterministic pass cites by finding, not by ref", func(m map[string]any) {
			m["kind"], m["verdict"], m["blocking"], m["model"] = "deterministic", "pass", false, nil
			m["state_refs"], m["activity_refs"], m["evidence_refs"], m["knowledge_refs"] = []any{}, []any{}, []any{}, []any{}
		}},
	}
	for _, tc := range good {
		if err := schema.Validate(mutate(t, root, "eval_result", tc.fn)); err != nil {
			t.Errorf("rejected %s: %v", tc.name, err)
		}
	}
}

func TestDecisionRankingAndKnowledgeUseKnownBadAreRejected(t *testing.T) {
	root := contractsDir(t)
	cases := map[string][]struct {
		name string
		fn   func(m map[string]any)
	}{
		"decision_ranking": {
			{"no preferred candidate", func(m map[string]any) { delete(m, "preferred_candidate_id") }},
			{"a preferred id that is not a uuid", func(m map[string]any) { m["preferred_candidate_id"] = "first" }},
			{"no pairwise reasons field", func(m map[string]any) { delete(m, "pairwise_reasons") }},
			{"a reason without text", func(m map[string]any) { cpObj(cpArr(m["pairwise_reasons"])[0])["reason"] = "" }},
			{"four candidates", func(m map[string]any) {
				m["order"] = append(cpArr(m["order"]), "0ca00000-0000-4000-8000-0000000000a4")
			}},
			{"a duplicate candidate in the order", func(m map[string]any) { o := cpArr(m["order"]); o[1] = o[0] }},
			{"an extra field", func(m map[string]any) { m["score"] = 0.9 }},
		},
		"knowledge_use": {
			{"influence is not a conformance", func(m map[string]any) { m["conformance"] = "influenced" }},
			{"an override without its reason", func(m map[string]any) { m["conformance"] = "overridden_with_reason" }},
			{"a reason on a followed clause", func(m map[string]any) { m["override_reason"] = "because" }},
			{"no guidance clause", func(m map[string]any) { delete(m, "guidance_clause") }},
			{"an unknown artifact field", func(m map[string]any) { cpObj(m["artifact_span"])["field"] = "footer" }},
			{"an extra field", func(m map[string]any) { m["influenced"] = true }},
		},
	}
	for name, list := range cases {
		schema := schemaFor(t, compiler(t, root), name)
		for _, tc := range list {
			if err := schema.Validate(mutate(t, root, name, tc.fn)); err == nil {
				t.Errorf("%s accepted: %s", name, tc.name)
			}
		}
	}
	schema := schemaFor(t, compiler(t, root), "knowledge_use")
	override := func(m map[string]any) {
		m["conformance"], m["override_reason"] = "overridden_with_reason", "the buyer asked for it"
	}
	if err := schema.Validate(mutate(t, root, "knowledge_use", override)); err != nil {
		t.Errorf("a stated override was rejected: %v", err)
	}
}

func TestJudgmentInferenceCarriesClassStrengthInstructionsAndUnknown(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "judgment_inference")
	good := func(m map[string]any) {
		d := cpObj(m["inferred_semantic_delta"])
		d["edit_class"], d["signal_strength"], d["unknown"] = []any{"cta", "timing"}, "moderate", false
		d["explicit_instructions"] = []any{map[string]any{"instruction": "Keep Priya on the thread.", "quote": "keep Priya on cc"}}
	}
	if err := schema.Validate(mutate(t, root, "judgment_inference", good)); err != nil {
		t.Fatalf("rejected a classified inference: %v", err)
	}
	for name, fn := range map[string]func(m map[string]any){
		"a class outside the vocabulary": func(m map[string]any) { cpObj(m["inferred_semantic_delta"])["edit_class"] = []any{"vibes"} },
		"a strength outside the scale":   func(m map[string]any) { cpObj(m["inferred_semantic_delta"])["signal_strength"] = "huge" },
		"an instruction without its quote": func(m map[string]any) {
			cpObj(m["inferred_semantic_delta"])["explicit_instructions"] = []any{map[string]any{"instruction": "x"}}
		},
	} {
		if err := schema.Validate(mutate(t, root, "judgment_inference", fn)); err == nil {
			t.Errorf("accepted: %s", name)
		}
	}
}

func TestTraceHasToolCallAndExecutionSpansAndTheCanonicalMutationOperations(t *testing.T) {
	root := contractsDir(t)
	var trace struct {
		Defs struct {
			SpanKind struct {
				Enum []string `json:"enum"`
			} `json:"spanKind"`
		} `json:"$defs"`
	}
	decodeFile(t, filepath.Join(root, "schemas", "episode_trace.v1.json"), &trace)
	for _, k := range []string{"tool_call", "execution"} {
		if !slices.Contains(trace.Defs.SpanKind.Enum, k) {
			t.Errorf("episode_trace lacks the %s span kind", k)
		}
	}
	var mut struct {
		Properties struct {
			Operation struct {
				Enum []string `json:"enum"`
			} `json:"operation"`
		} `json:"properties"`
	}
	decodeFile(t, filepath.Join(root, "schemas", "knowledge_mutation.v1.json"), &mut)
	want := []string{"CREATE", "STRENGTHEN", "WEAKEN", "REFINE", "NARROW", "EXPAND", "ADD_EXCEPTION", "DISPUTE", "MARK_STALE", "NO_CHANGE"}
	if !slices.Equal(mut.Properties.Operation.Enum, want) {
		t.Errorf("mutation operations = %v, want HAR-97's %v", mut.Properties.Operation.Enum, want)
	}
}
