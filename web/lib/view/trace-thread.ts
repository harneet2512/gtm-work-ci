// The Cliff thread (Slack message 1, 2, 3) for the trace view's Thread tab, from the same payloads the Cliff mode reads. A
// message with nothing recorded is left out, never made up.
import type { BusinessIntelligence, HumanStrategyDecision, JudgmentInference, RunStrategies } from "@/lib/api/types";

export interface ThreadMessage {
  id: "M1" | "M2" | "M3";
  title: string;
  lines: string[];
}

export function buildThread(input: {
  bi: BusinessIntelligence | null;
  strategies: RunStrategies | null;
  decision: HumanStrategyDecision | null;
  inference: JudgmentInference | null;
}): ThreadMessage[] {
  const out: ThreadMessage[] = [];
  if (input.bi) out.push({ id: "M1", title: "Business intelligence", lines: [input.bi.summary, ...(input.bi.why_it_matters ? [input.bi.why_it_matters] : [])] });
  const cands = input.strategies?.strategy_set?.candidates ?? [];
  if (cands.length > 0) {
    out.push({
      id: "M2",
      title: "Strategy chooser",
      lines: [
        `gtm_ai drafted ${cands.length} moves`,
        ...cands.map((c, i) => `${String.fromCharCode(65 + i)}. ${c.title ?? c.strategy_type}${c.preferred_by_agent ? " (gtm_ai's pick)" : ""}${c.candidate_id === input.decision?.selected_candidate_id ? " (chosen)" : ""}`),
      ],
    });
  }
  if (input.inference) out.push({ id: "M3", title: "What the judgment taught", lines: [`The person ${input.inference.agreement} gtm_ai's pick`, input.inference.inferred_semantic_delta.statement] });
  return out;
}
