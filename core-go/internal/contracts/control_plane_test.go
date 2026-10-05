package contracts

import "testing"

// HAR-145 control-plane read contracts: the known-bad mutations each schema must refuse. The examples themselves are
// covered by TestEveryExampleValidates; the endpoints that serve them by internal/api/contracttest.

type cpCase struct {
	name string
	fn   func(m map[string]any)
}

func rejectAll(t *testing.T, schemaName string, cases []cpCase) {
	t.Helper()
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), schemaName)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, schemaName, tc.fn)); err == nil {
				t.Fatalf("accepted: %s", tc.name)
			}
		})
	}
}

func acceptAll(t *testing.T, schemaName string, cases []cpCase) {
	t.Helper()
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), schemaName)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, schemaName, tc.fn)); err != nil {
				t.Fatalf("rejected %s: %v", tc.name, err)
			}
		})
	}
}

func cpObj(v any) map[string]any { return v.(map[string]any) }
func cpArr(v any) []any          { return v.([]any) }

func TestEpisodeSummaryKnownBadAreRejected(t *testing.T) {
	rejectAll(t, "episode_summary", []cpCase{
		{"a field is missing", func(m map[string]any) { delete(m, "human_outcome") }},
		{"an unknown episode status", func(m map[string]any) { m["status"] = "closed" }},
		{"an unknown final status", func(m map[string]any) { m["final_status"] = "done" }},
		{"sent is only for a live run: the old dry-run wording is its own value", func(m map[string]any) { m["final_status"] = "sent_dry_run" }},
		{"a candidate ranks 1..3", func(m map[string]any) { cpObj(m["selected_action"])["ranking"] = 4 }},
		{"agreement is agreed or overrode", func(m map[string]any) { cpObj(m["human_outcome"])["agreement"] = "maybe" }},
		{"an unknown human action", func(m map[string]any) { cpObj(m["human_outcome"])["human_action"] = "SHRUG" }},
		{"an unknown judgment status", func(m map[string]any) { m["judgment_status"] = "unsure" }},
		{"the trigger activity count is at least 1", func(m map[string]any) { cpObj(m["triggering_event"])["trigger_activity_count"] = 0 }},
		{"no extra fields", func(m map[string]any) { m["score"] = 0.9 }},
	})
}

func TestEpisodeSummaryEarlyEpisodeShapesAreAccepted(t *testing.T) {
	acceptAll(t, "episode_summary", []cpCase{
		{"awaiting the human's choice", func(m map[string]any) {
			m["status"], m["final_status"] = "awaiting_choice", "awaiting_choice"
			m["selected_action"], m["human_outcome"], m["judgment_status"], m["replay"] = nil, nil, "none", nil
		}},
		{"an episode without a strategy set or trigger", func(m map[string]any) {
			m["strategy_set_id"], m["triggering_event"], m["recommended_action"] = nil, nil, nil
		}},
		{"chosen, send pending", func(m map[string]any) {
			m["final_status"] = "awaiting_send"
			o := cpObj(m["human_outcome"])
			o["human_action"], o["send_decision"], o["send_decided_at"] = nil, "pending", nil
		}},
	})
}

func TestEpisodeTraceKnownBadAreRejected(t *testing.T) {
	first := func(m map[string]any) map[string]any { return cpObj(cpArr(m["spans"])[0]) }
	rejectAll(t, "episode_trace", []cpCase{
		{"a trace has spans", func(m map[string]any) { m["spans"] = []any{} }},
		{"an unknown span kind", func(m map[string]any) { first(m)["kind"] = "vibes" }},
		{"an unknown span status", func(m map[string]any) { first(m)["status"] = "ok" }},
		{"a span id is kind:ref", func(m map[string]any) { first(m)["id"] = "Not A Stable Id" }},
		{"seq starts at 1", func(m map[string]any) { first(m)["seq"] = 0 }},
		{"a span carries its eval result ids", func(m map[string]any) { delete(first(m), "eval_result_ids") }},
		{"a ref names a known kind", func(m map[string]any) { first(m)["refs"] = []any{map[string]any{"kind": "spreadsheet", "id": "x"}} }},
		{"eval result ids are uuids", func(m map[string]any) { first(m)["eval_result_ids"] = []any{"not-a-uuid"} }},
		{"a span carries no score", func(m map[string]any) { first(m)["score"] = 0.5 }},
		{"unassigned results are listed", func(m map[string]any) { delete(m, "unassigned_eval_result_ids") }},
	})
}

func TestEvalRunKnownBadAreRejected(t *testing.T) {
	rejectAll(t, "eval_run", []cpCase{
		{"all four areas are listed", func(m map[string]any) { m["areas"] = cpArr(m["areas"])[:3] }},
		{"an unknown area", func(m map[string]any) { cpObj(cpArr(m["areas"])[0])["area"] = "growth" }},
		{"counts are never negative", func(m map[string]any) { cpObj(m["counts"])["fail"] = -1 }},
		{"a count is required", func(m map[string]any) { delete(cpObj(m["counts"]), "unknown") }},
		{"no aggregate percentage", func(m map[string]any) { m["pass_rate"] = 0.5 }},
		{"no percentage inside the counts", func(m map[string]any) { cpObj(m["counts"])["pass_percent"] = 50 }},
		{"a run has at least one result", func(m map[string]any) { m["result_count"] = 0 }},
	})
}

func TestEvalRunFirstRunHasNoDeltas(t *testing.T) {
	acceptAll(t, "eval_run", []cpCase{
		{"first run of the account", func(m map[string]any) {
			m["previous_eval_run_id"] = nil
			for _, a := range cpArr(m["areas"]) {
				cpObj(a)["delta"] = nil
			}
		}},
		{"a negative delta is a decrease", func(m map[string]any) { cpObj(cpObj(cpArr(m["areas"])[1])["delta"])["fail"] = -3 }},
	})
}

func TestEvalFamilySummaryKnownBadAreRejected(t *testing.T) {
	fam := func(m map[string]any) map[string]any { return cpObj(cpArr(cpObj(cpArr(m["areas"])[1])["families"])[0]) }
	rejectAll(t, "eval_family_summary", []cpCase{
		{"a family id is E<n>", func(m map[string]any) { fam(m)["family_id"] = "M1" }},
		{"an eval type is from the catalog", func(m map[string]any) { cpObj(cpArr(fam(m)["eval_types"])[0])["eval_type"] = "vibes" }},
		{"all four areas", func(m map[string]any) { m["areas"] = cpArr(m["areas"])[:2] }},
		{"no family score", func(m map[string]any) { fam(m)["score"] = 0.8 }},
		{"counts are required on a family", func(m map[string]any) { delete(fam(m), "counts") }},
	})
}

func TestEvalRunComparisonKnownBadAreRejected(t *testing.T) {
	row := func(m map[string]any) map[string]any { return cpObj(cpArr(m["rows"])[0]) }
	rejectAll(t, "eval_run_comparison", []cpCase{
		{"abstain is spelled unknown", func(m map[string]any) { row(m)["a"] = "abstain" }},
		{"not_relevant is not a verdict here", func(m map[string]any) { row(m)["b"] = "not_relevant" }},
		{"an unknown change", func(m map[string]any) { row(m)["change"] = "better" }},
		{"the overall change has no added or removed", func(m map[string]any) { cpObj(m["overall"])["change"] = "added" }},
		{"the overall row counts inconclusive rows", func(m map[string]any) { delete(cpObj(m["overall"]), "inconclusive") }},
		{"the overall row counts newly failing rows", func(m map[string]any) { delete(cpObj(m["overall"]), "added_fail") }},
		{"both sides are named", func(m map[string]any) { delete(m, "b") }},
		{"a row names its blocking flags", func(m map[string]any) { delete(row(m), "a_blocking") }},
	})
}

func TestEvalRunComparisonOneSidedRowsAreAccepted(t *testing.T) {
	acceptAll(t, "eval_run_comparison", []cpCase{
		{"an eval type only B checked", func(m map[string]any) {
			r := cpRow2(m)
			r["a"], r["change"], r["a_result_ids"], r["a_blocking"] = nil, "added", []any{}, false
		}},
		{"an eval type only A checked", func(m map[string]any) {
			r := cpRow2(m)
			r["b"], r["change"], r["b_result_ids"], r["b_blocking"] = nil, "removed", []any{}, false
		}},
	})
}

func cpRow2(m map[string]any) map[string]any { return cpObj(cpArr(m["rows"])[1]) }

func TestKnowledgeMutationKnownBadAreRejected(t *testing.T) {
	rejectAll(t, "knowledge_mutation", []cpCase{
		{"an unknown operation", func(m map[string]any) { m["operation"] = "DELETE" }},
		{"a version starts at 1", func(m map[string]any) { m["version"] = 0 }},
		{"an unknown scope", func(m map[string]any) { m["scope"] = "global" }},
		{"an unknown human verdict", func(m map[string]any) { m["human_verdict"] = "approved" }},
		{"an unknown evidence kind", func(m map[string]any) { cpObj(m["evidence"])["kind"] = "hunch" }},
		{"an unknown status", func(m map[string]any) { m["status"] = "retired" }},
		{"a key is K<n>", func(m map[string]any) { m["knowledge_key"] = "17" }},
		{"the evidence episode is named", func(m map[string]any) { delete(m, "evidence_episode_id") }},
		{"a precondition is a condition", func(m map[string]any) { m["preconditions"] = []any{"always"} }},
	})
}

func TestKnowledgeMutationCreateShapeIsAccepted(t *testing.T) {
	acceptAll(t, "knowledge_mutation", []cpCase{
		{"a CREATE has no earlier status and no human verdict yet", func(m map[string]any) {
			m["operation"], m["status_before"], m["status"], m["version"] = "CREATE", nil, "candidate", 1
			m["human_verdict"], m["human_confirmed"], m["knowledge_key"] = "none", false, nil
		}},
		{"a reserved REFINE", func(m map[string]any) { m["operation"] = "REFINE" }},
		{"a demotion down the status ladder", func(m map[string]any) { m["operation"] = "DEMOTE" }},
	})
}

func TestOperationalMetricsKnownBadAreRejected(t *testing.T) {
	stage := func(m map[string]any) map[string]any { return cpObj(cpArr(m["stages"])[0]) }
	rejectAll(t, "operational_metrics", []cpCase{
		{"a metric is never classified as an eval", func(m map[string]any) { m["classification"] = "eval" }},
		{"classification is required", func(m map[string]any) { delete(m, "classification") }},
		{"no verdict on a metric", func(m map[string]any) { m["verdict"] = "pass" }},
		{"no score on a metric", func(m map[string]any) { m["score"] = 0.9 }},
		{"tokens are never negative", func(m map[string]any) { m["input_tokens"] = -1 }},
		{"an unknown stage", func(m map[string]any) { stage(m)["stage"] = "extract" }},
		{"a stage has at least one worker call", func(m map[string]any) { stage(m)["worker_calls"] = 0 }},
		{"latency names both worker-call and model time", func(m map[string]any) { delete(cpObj(m["latency"]), "model_ms") }},
		{"the old wall_ms name is gone: it was a sum of worker calls, not wall time", func(m map[string]any) {
			cpObj(m["latency"])["wall_ms"] = 1
		}},
		{"usage_source is required", func(m map[string]any) { delete(m, "usage_source") }},
		{"usage_source is live, replay or mixed", func(m map[string]any) { m["usage_source"] = "estimated" }},
		{"cost is a number or null", func(m map[string]any) { m["cost_usd"] = "free" }},
	})
}

func TestWorkerUsageKnownBadAreRejected(t *testing.T) {
	rejectAll(t, "worker_usage", []cpCase{
		{"tokens are never negative", func(m map[string]any) { m["input_tokens"] = -1 }},
		{"every count is required", func(m map[string]any) { delete(m, "retries") }},
		{"cost is a number or null", func(m map[string]any) { m["cost_usd"] = "free" }},
		{"cost is never negative", func(m map[string]any) { m["cost_usd"] = -0.01 }},
		{"a model call count is an integer", func(m map[string]any) { m["model_calls"] = 1.5 }},
		{"models are unique", func(m map[string]any) { m["models"] = []any{"a", "a"} }},
		{"usage_source is required", func(m map[string]any) { delete(m, "usage_source") }},
		{"usage_source is live or replay", func(m map[string]any) { m["usage_source"] = "mixed" }},
		{"no verdict on usage", func(m map[string]any) { m["verdict"] = "pass" }},
	})
	acceptAll(t, "worker_usage", []cpCase{
		{"a provider that reported no cost", func(m map[string]any) { m["cost_usd"] = nil }},
		{"a provider that reported no cached or reasoning tokens", func(m map[string]any) {
			m["cached_input_tokens"], m["reasoning_tokens"] = nil, nil
		}},
		{"a replayed request spent nothing", func(m map[string]any) {
			m["usage_source"], m["model_calls"], m["input_tokens"], m["output_tokens"] = "replay", 0, 0, 0
			m["cached_input_tokens"], m["reasoning_tokens"], m["cost_usd"] = nil, nil, nil
			m["tool_calls"], m["retries"], m["model_ms"], m["models"] = 0, 0, 0, []any{}
		}},
		{"a request with no model call", func(m map[string]any) {
			m["model_calls"], m["input_tokens"], m["output_tokens"], m["cached_input_tokens"], m["reasoning_tokens"] = 0, 0, 0, 0, 0
			m["tool_calls"], m["retries"], m["model_ms"], m["models"] = 0, 0, 0, []any{}
		}},
	})
}

func TestOperationalMetricsUnmeasuredShapeIsAccepted(t *testing.T) {
	acceptAll(t, "operational_metrics", []cpCase{
		{"no usage recorded", func(m map[string]any) {
			m["measured"], m["model_calls"], m["input_tokens"], m["output_tokens"] = false, 0, 0, 0
			m["cached_input_tokens"], m["reasoning_tokens"], m["tool_calls"], m["retries"] = nil, nil, 0, 0
			m["cost_usd"], m["models"], m["stages"], m["decision_episode_id"] = nil, []any{}, []any{}, nil
			m["latency"] = map[string]any{"worker_call_ms": 0, "model_ms": 0}
			m["usage_source"] = nil
		}},
		{"replay only: not measured, nothing summed", func(m map[string]any) {
			m["measured"], m["usage_source"], m["model_calls"], m["input_tokens"], m["output_tokens"] = false, "replay", 0, 0, 0
			m["cached_input_tokens"], m["reasoning_tokens"], m["cost_usd"], m["stages"], m["models"] = nil, nil, nil, []any{}, []any{}
			m["tool_calls"], m["retries"] = 0, 0
			m["latency"] = map[string]any{"worker_call_ms": 0, "model_ms": 0}
		}},
	})
}
