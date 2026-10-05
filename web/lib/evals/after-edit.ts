// After an edit (eval design spec 4b-3 Level 2, HAR-139): the affected evals are re-run on the final artifact
// before Send, and the page shows what changed ("CTA calibration WARN → PASS after your edit"). Until the core
// serves those send-time results the page says "Not re-evaluated yet" and keeps the draft's open verdicts on
// record; it never guesses a delta. A blocking failure on the latest verdicts explains why Send is blocked.
import type { EvalResult, HumanStrategyDecision, LiteralChange } from "@/lib/api/types";
import type { CandidateChain } from "@/lib/view/run-chain";
import { canonicalVerdict, evalName, fill, verdictWording, type BundleVerdict, type CellVerdict } from "./vocabulary";

/** Send-time EvalResults on the final artifact (HAR-139 persists them to eval_runs at send). */
export interface ReEvaluation {
  evaluatedAt: string;
  results: readonly EvalResult[];
}

export type AfterEditState = "no_decision" | "not_chosen" | "unedited" | "not_reevaluated" | "reevaluated";

export interface VerdictDelta {
  evalType: string;
  name: string;
  from: CellVerdict;
  to: BundleVerdict;
  text: string;
}

export interface AfterEditView {
  state: AfterEditState;
  /** "Dana Kim changed the call to action and edited a paragraph." */
  summary: string | null;
  edits: string[];
  /** The draft's verdicts that did not pass and still stand because nothing re-ran them. */
  standing: { name: string; verdict: BundleVerdict }[];
  deltas: VerdictDelta[];
  unchanged: number;
  evaluatedAt: string | null;
  sendBlocked: { name: string; reason: string }[];
  sendBlockedTitle: string | null;
  sent: { at: string | null } | null;
  discarded: boolean;
}

const EDIT_WORDS: Readonly<Record<LiteralChange["kind"], string>> = {
  recipient_added: "added a recipient",
  recipient_removed: "removed a recipient",
  recipient_role_changed: "moved a recipient between To and CC",
  subject_changed: "changed the subject",
  cta_changed: "changed the call to action",
  timing_changed: "changed the timing",
  paragraph_added: "added a paragraph",
  paragraph_removed: "removed a paragraph",
  paragraph_edited: "edited a paragraph",
  channel_changed: "changed the channel",
  action_type_changed: "changed the kind of action",
  attachment_added: "added an attachment",
  attachment_removed: "removed an attachment",
  crm_next_step_changed: "changed the CRM next step",
};

export function editPhrases(edits: readonly Pick<LiteralChange, "kind">[]): string[] {
  return [...new Set(edits.map((e) => EDIT_WORDS[e.kind]))];
}

function joinPlain(parts: readonly string[]): string {
  if (parts.length <= 1) return parts[0] ?? "";
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

const upper = (v: CellVerdict): string => verdictWording(v).label.toUpperCase();

function deltasOf(chain: CandidateChain, reeval: ReEvaluation): { deltas: VerdictDelta[]; unchanged: number } {
  const before = new Map((chain.bundle?.items ?? []).map((i) => [i.eval_type as string, canonicalVerdict(i.verdict) as CellVerdict]));
  const deltas: VerdictDelta[] = [];
  let unchanged = 0;
  for (const r of reeval.results) {
    const from = before.get(r.eval_type) ?? "not_checked";
    const to = canonicalVerdict(r.verdict);
    if (from === to) {
      unchanged += 1;
      continue;
    }
    const name = evalName(r.eval_type);
    deltas.push({ evalType: r.eval_type, name, from, to, text: fill("after_edit", { eval: name, from: upper(from), to: upper(to) }) });
  }
  return { deltas, unchanged };
}

/** Blocking failures on the latest verdicts: the send-time results when they exist, else the draft's. */
function blockingOf(chain: CandidateChain, reeval: ReEvaluation | null): { name: string; reason: string }[] {
  const results = reeval ? reeval.results : (chain.bundle?.items ?? []).flatMap((i) => (i.result ? [i.result] : []));
  return results.filter((r) => r.verdict === "fail" && r.blocking).map((r) => ({ name: evalName(r.eval_type), reason: r.reason }));
}

const EMPTY: Omit<AfterEditView, "state"> = {
  summary: null,
  edits: [],
  standing: [],
  deltas: [],
  unchanged: 0,
  evaluatedAt: null,
  sendBlocked: [],
  sendBlockedTitle: null,
  sent: null,
  discarded: false,
};

export function buildAfterEdit(chain: CandidateChain, decision: HumanStrategyDecision | null, reeval: ReEvaluation | null): AfterEditView {
  if (!decision) return { state: "no_decision", ...EMPTY };
  if (decision.selected_candidate_id !== chain.candidate.candidate_id) return { state: "not_chosen", ...EMPTY };

  const edits = editPhrases(decision.edits);
  const pending = decision.send_decision === "pending";
  const sendBlocked = pending ? blockingOf(chain, reeval) : [];
  const common = {
    ...EMPTY,
    edits,
    summary: edits.length > 0 ? `${decision.actor_label} ${joinPlain(edits)}.` : null,
    sendBlocked,
    sendBlockedTitle: sendBlocked[0] ? fill("send_blocked", { eval: sendBlocked[0].name }) : null,
    sent: decision.send_decision === "send" ? { at: decision.send_decided_at ?? null } : null,
    discarded: decision.send_decision === "discard",
  };
  if (reeval) return { ...common, state: "reevaluated", ...deltasOf(chain, reeval), evaluatedAt: reeval.evaluatedAt };
  if (edits.length === 0) return { ...common, state: "unedited" };
  const standing = (chain.bundle?.items ?? [])
    .filter((i) => i.verdict !== "pass" && i.verdict !== "not_relevant")
    .map((i) => ({ name: evalName(i.eval_type), verdict: canonicalVerdict(i.verdict) }));
  return { ...common, state: "not_reevaluated", standing };
}
