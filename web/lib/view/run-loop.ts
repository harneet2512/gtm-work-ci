// The run's decision loop (HAR-145): retrieve → reason → rank → act → learn, derived from the trace,
// the strategy set, the human decision and the inference. Each phase reports what was recorded:
// "recorded" is not an eval PASS; a run that never learned says so; nothing infers a stage's success
// from a later stage existing.
import type { AgentRun, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import type { NodeStatus } from "./episode";

export interface LoopPhase {
  id: "retrieve" | "reason" | "rank" | "act" | "learn";
  label: string;
  status: NodeStatus;
  summary: string;
  detail: string | null;
}

const phase = (id: LoopPhase["id"], status: NodeStatus, summary: string, detail: string | null = null): LoopPhase => ({
  id,
  label: id.charAt(0).toUpperCase() + id.slice(1),
  status,
  summary,
  detail,
});

const count = (n: number, word: string) => `${n} ${n === 1 ? word : word.endsWith("y") ? `${word.slice(0, -1)}ies` : `${word}s`}`;

function retrieve(t: RunTrace | null, run: AgentRun): LoopPhase {
  const accesses = (t?.context_accesses ?? []) as { tool: string; items?: unknown[] }[];
  const knowledgeRefs = run.knowledge_refs_used ?? [];
  if (accesses.length === 0 && knowledgeRefs.length === 0)
    return phase("retrieve", "absent", "No context pull recorded for this run.");
  const tools = accesses.map((a) => a.tool).join(", ");
  const extras = knowledgeRefs.length > 0 ? ` · ${count(knowledgeRefs.length, "knowledge item")} cited` : "";
  return phase("retrieve", "recorded", `Context read from ${count(accesses.length, "tool")} (${tools})${extras}.`, `${count(accesses.reduce((n, a) => n + (a.items?.length ?? 0), 0), "item")} returned`);
}

function reason(t: RunTrace | null): LoopPhase {
  const eval_ = t?.trigger_evaluation;
  const signals = t?.signals ?? [];
  if (!eval_ && signals.length === 0) return phase("reason", "absent", "No trigger evaluation or signals on the trace.");
  if (eval_ && eval_.eligible === false)
    return phase("reason", "recorded", `The run was found ineligible — ${eval_.explanation ?? ((eval_.reason_codes ?? []).join(", ") || "no reason recorded")}.`);
  const why = eval_?.explanation ?? "";
  return phase("reason", "recorded", `${count(signals.length, "signal")} fired${why ? ` — ${why}` : "."}`, eval_ ? `evaluation ${eval_.id.slice(0, 13)}… · ${(eval_.reason_codes ?? []).join(", ")}` : null);
}

function rank(strategies: RunStrategies | null): LoopPhase {
  const cs = strategies?.strategy_set?.candidates ?? [];
  if (cs.length === 0)
    return phase("rank", strategies?.strategy_set?.no_acceptable_candidate ? "recorded" : "absent", strategies?.strategy_set?.no_acceptable_candidate ? "Every candidate was blocked — no acceptable option." : "No strategy set produced.");
  const pick = cs.find((c) => c.preferred_by_agent);
  const evalCount = (strategies?.eval_bundles ?? []).reduce((n, b) => n + (b.items ?? []).length, 0);
  return phase(
    "rank",
    "recorded",
    pick ? `${count(cs.length, "candidate")} ranked — Ghost's pick: ${pick.title ?? pick.candidate_id}.` : `${count(cs.length, "candidate")} produced with no preferred mark.`,
    evalCount > 0 ? `${count(evalCount, "eval result")} across the candidates` : "no eval bundles",
  );
}

function act(run: AgentRun, t: RunTrace | null, decision: HumanStrategyDecision | null): LoopPhase {
  const human = t?.decisions?.[0];
  const out = run.output;
  if (!out?.proposed_action_type && !human) return phase("act", "waiting", "No action was produced or confirmed.");
  const sent = run.status === "recorded" || Boolean((out as { external_effect_id?: string | null } | null)?.external_effect_id);
  const edited = human?.decision === "edit";
  return phase(
    "act",
    sent ? "recorded" : "waiting",
    `${out?.proposed_action_type ?? "action"}${human ? ` after the human's ${human.decision}` : ""}${edited ? " — edited before send" : ""}${sent ? ", recorded as executed" : " — awaiting execution evidence"}.`,
    out?.finished_artifact ? `${(out.finished_artifact as { channel?: string }).channel ?? "draft"} · ${out.recipients?.length ?? 0} recipients` : null,
  );
}

function learn(t: RunTrace | null, inference: JudgmentInference | null): LoopPhase {
  const updates = (t?.placeholders as { knowledge_updates?: unknown[] } | undefined)?.knowledge_updates ?? [];
  if (updates.length > 0) return phase("learn", "recorded", `${count(updates.length, "knowledge update")} written back.`);
  if (inference) return phase("learn", "waiting", `Judgment inferred — the human ${inference.agreement} the pick — but no confirmed mutation yet.`);
  return phase("learn", "absent", "No learning recorded for this run.");
}

/** The five phases in loop order — the strip above the run's detail. */
export function buildLoop(run: AgentRun, trace: RunTrace | null, strategies: RunStrategies | null, decision: HumanStrategyDecision | null, inference: JudgmentInference | null): LoopPhase[] {
  return [retrieve(trace, run), reason(trace), rank(strategies), act(run, trace, decision), learn(trace, inference)];
}
