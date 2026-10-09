// Shared builders for the HAR-149 inspector tests: gate results shaped like GET /episodes/{id}/gate-results, a registry
// gate definition and the registry's control effect rules. Plain values; nothing here calls a backend.
import type { GateResult } from "@/lib/api/types";
import type { GateDefinitionSource } from "@/lib/evals/gate-cards";
import { effectForVerdict, type EffectRules } from "@/lib/evals/inspector/effects";

export const EP = "0e7a1000-0000-4000-8000-0000000000e1";
export const CAND_A = "0e7a1000-0000-4000-8000-0000000000ca";
export const CAND_B = "0e7a1000-0000-4000-8000-0000000000cb";
export const CAND_C = "0e7a1000-0000-4000-8000-0000000000cc";

let n = 0;
export function result(over: Partial<GateResult> & { gate: string }): GateResult {
  n += 1;
  const base = {
    id: `0e7a1000-0000-4000-8000-${String(n).padStart(12, "0")}`,
    sub_gate: "",
    label: "",
    judged_object: { type: "DecisionEpisode", id: EP },
    span_id: `candidates:${EP}`,
    verdict: "pass",
    question: "A question?",
    observed: "observed",
    why: "because",
    evidence_refs: [`candidate:${CAND_A}`],
    improves: "improves",
    grader: { kind: "deterministic" },
    calibrated: false,
    criteria: [],
    evaluator_version: `${over.gate}:deterministic:v1`,
    lineage: {},
    latency_ms: null,
    model_calls: null,
    tokens: null,
    cost_usd: null,
    ...over,
  } as GateResult;
  return { ...base, control_effect: over.control_effect ?? effectForVerdict(RULES, base.gate, base.verdict) } as GateResult;
}

export const RULES: EffectRules = {
  vocabulary: ["OBSERVE", "CONTINUE", "RANK", "RECOMPUTE", "RETRY", "BLOCK", "ESCALATE", "MARK UNKNOWN", "RECORD ONLY"],
  description: "rules",
  default: { pass: "RECORD ONLY", warn: "RECORD ONLY", fail: "RECORD ONLY", unknown: "MARK UNKNOWN" },
  gates: {
    D8: { pass: "CONTINUE", fail: "BLOCK", only_for: { grader: "deterministic", sub_gates: ["recipients", "no_stale_content"] }, basis: "strategystore/presend_d8.go d8AsBlockingEvals" },
    B9: { basis: "knowledge/lifecycle.go:117 holds knowledge by its own predicate; the B9 result only records" },
  },
};

export function def(id: string, over: Partial<GateDefinitionSource> = {}): GateDefinitionSource {
  return {
    id,
    bucket: id.startsWith("B") ? "context_intelligence" : id.startsWith("D") ? "decision_action" : "system_health",
    order: 1,
    name: `${id} long name`,
    question: "A long question?",
    improves: "It improves things.",
    display_name: `${id} display`,
    display_question: `Is ${id} right?`,
    plain_what: `We check what ${id} does, in plain words.`,
    plain_why: `If ${id} fails, something bad slips through.`,
    invariant: "A bad behaviour",
    judges: "Something, at the Ranking step",
    grader: "model",
    criteria: { pass: "all good.", warn: "partly good.", fail: "bad.", unknown: "no data." },
    protects: "It protects.",
    mode: "live_required",
    trigger: "when a recommendation is ranked",
    message: "M2",
    impact: "monitoring only",
    impact_basis: "No code reads this gate's stored result; the verdict is recorded and shown only.",
    ...over,
  };
}
