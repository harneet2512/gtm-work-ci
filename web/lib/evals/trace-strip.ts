// The trace strip of the eval page (HAR-97 "Internal trace viewer", condensed): the decision's chain from
// Event N to the inference, one step each, each linking to the page that shows it. Steps say plainly what has
// not happened yet; nothing is inferred that the recorded objects do not say.
import type { RunTrace } from "@/lib/api/types";
import type { EvalPageData } from "@/lib/load-eval-page";
import { buildChain } from "@/lib/view/run-chain";
import { buildAfterEdit } from "./after-edit";
import { sourceWord } from "./evidence";
import { buildMatrix } from "./matrix";

export type TraceStepKey = "event" | "state" | "strategies" | "evals" | "choice" | "send" | "inference";
/** done: recorded; current: this page; waiting: not happened yet; blocked: held by a failing check; none: not recorded. */
export type TraceStepStatus = "done" | "current" | "waiting" | "blocked" | "none";

export interface TraceStep {
  key: TraceStepKey;
  label: string;
  detail: string;
  /** ISO instant the step happened, when recorded. */
  at: string | null;
  href: string | null;
  status: TraceStepStatus;
}

const step = (key: TraceStepKey, label: string, detail: string, status: TraceStepStatus, href: string | null, at: string | null = null): TraceStep => ({
  key,
  label,
  detail,
  at,
  href,
  status,
});

function eventStep(trace: RunTrace | null, accountId: string | null): TraceStep {
  const a = trace?.trigger_activities[0];
  if (!a) return step("event", "Event N", "Not recorded", "none", null);
  const from = a.participants.find((p) => p.role === "from")?.display_name;
  const word = sourceWord(a.activity_type);
  const href = accountId ? `/accounts/${accountId}?event=${a.source_event_id}&view=after` : null;
  return step("event", "Event N", from ? `${word} from ${from}` : word, "done", href, a.occurred_at);
}

function stateStep(trace: RunTrace | null): TraceStep {
  const diff = trace?.state_diff;
  if (!diff) return step("state", "State change", "Not recorded", "none", null);
  const material = diff.changes.filter((c) => c.material).length;
  const detail = diff.is_material && material > 0 ? `${material} material change${material === 1 ? "" : "s"}` : "No material change";
  return step("state", "State change", detail, "done", "#intelligence-h", diff.created_at);
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

export function buildTraceSteps(data: EvalPageData): TraceStep[] {
  const runId = data.run.id;
  const { strategies, decision, inference } = data;
  const chain = buildChain(strategies);
  const matrix = buildMatrix(chain, decision);
  const verdicts = chain.reduce((n, c) => n + c.counts.fail + c.counts.warn + c.counts.abstain + c.counts.pass, 0);
  const human = `/runs/${runId}#human-h`;

  const chosen = matrix.columns.find((c) => c.isChosen);
  const choice = decision
    ? step("choice", "Human choice", `${decision.actor_label} chose ${chosen?.isGhostPick ? "gtm_ai's pick" : (chosen?.letter ?? "an option")}`, "done", human, decision.chosen_at)
    : step("choice", "Human choice", "Awaiting a choice", "waiting", human);

  let send = step("send", "Send", "After a choice", "waiting", human);
  if (decision?.send_decision === "send") send = step("send", "Send", "Sent", "done", human, decision.send_decided_at ?? null);
  else if (decision?.send_decision === "discard") send = step("send", "Send", "Discarded", "done", human, decision.send_decided_at ?? null);
  else if (decision) {
    const picked = chain.find((c) => c.candidate.candidate_id === decision.selected_candidate_id);
    const blocked = picked ? buildAfterEdit(picked, decision, data.reevaluation).sendBlocked[0] : undefined;
    send = blocked ? step("send", "Send", `Blocked: ${blocked.name}`, "blocked", human) : step("send", "Send", "Not sent yet", "waiting", human);
  }

  const why = `/runs/${runId}#why-h`;
  const verdictWords = { pending: "Awaiting confirmation", confirmed: "Confirmed by the human", corrected: "Corrected by the human", no_learning: "Learning declined by the human" } as const;
  const inferenceStep = inference
    ? step("inference", "Inference", verdictWords[inference.human_verdict], inference.human_verdict === "pending" ? "waiting" : "done", why, inference.generated_at)
    : step("inference", "Inference", "After the send", "waiting", why);

  return [
    eventStep(data.trace, data.run.account_id ?? null),
    stateStep(data.trace),
    strategies
      ? step("strategies", "Strategies", plural(strategies.strategy_set.candidates.length, "option"), "done", `/runs/${runId}#candidates-h`, strategies.strategy_set.generated_at)
      : step("strategies", "Strategies", "Not drafted yet", "waiting", `/runs/${runId}#candidates-h`),
    step("evals", "Evals", verdicts > 0 ? plural(verdicts, "verdict") : "No verdicts yet", "current", "#compare-h"),
    choice,
    send,
    inferenceStep,
  ];
}
