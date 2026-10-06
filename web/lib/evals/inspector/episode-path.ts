// The live episode path (HAR-149 section 2): the gates that apply to one episode, in causal order, each with its question,
// what happened to it (ran, passed, failed, not applicable, waiting for its trigger, retried, recomputed, overridden) and
// the control effect it had. The human's edit sits inline with the gates it caused to run again. Everything is read from
// the episode summary and the stored results; a gate with no stored result is never shown as passed.
import type { DependencyInvalidation, EpisodeSummary, GateResult } from "@/lib/api/types";
import type { GateDefinitionSource } from "@/lib/evals/gate-cards";
import { controlEffectView, type EffectRules, type EffectView } from "./effects";

/** B1-B9, then the decision flow in the order it happens: D4 and D5 read the choice and the edit, D6 asks whether an existing check had already flagged the correction, D7 recomputes, D8 checks the message before it goes out, D9 confirms the delivery and D10 the feedback. */
export const PATH_ORDER: readonly string[] = ["B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9", "D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"];

export type PathStatus = "passed" | "failed" | "ran" | "not_applicable" | "waiting" | "not_recorded" | "retried" | "recomputed" | "overridden";

export interface PathGateNode {
  type: "gate";
  gate: string;
  /** The question in human words; the id is secondary. */
  question: string;
  status: PathStatus;
  statusLabel: string;
  verdict: "pass" | "warn" | "fail" | "unknown" | null;
  /** What the result did; null when the gate has no result. */
  effect: Pick<EffectView, "headline" | "label" | "hardStop"> | null;
  mode: string;
  message: string | null;
}

export interface PathEditNode {
  type: "edit";
  text: string;
  /** What the recompute engine says the edit made stale and re-derived (its own records, in words); empty when none is recorded. */
  recomputed: string[];
}

export type PathItem = PathGateNode | PathEditNode;

export interface EpisodePath {
  items: PathItem[];
  counts: { total: number; ran: number; failed: number; warned: number; waiting: number; notApplicable: number };
}

type Trigger = "occurred" | "absent" | "pending";
const FINISHED = new Set(["sent", "send_recorded"]);

/** Whether the situation a gate waits for has happened in this episode. */
function triggerOf(gate: string, s: EpisodeSummary): Trigger {
  const o = s.human_outcome;
  switch (gate) {
    case "D4":
    case "B9":
      return o ? "occurred" : "pending";
    case "D5":
    case "D6":
    case "D7":
      return !o ? "pending" : o.edited ? "occurred" : "absent";
    case "D8":
      return o && o.send_decision !== "pending" ? "occurred" : "pending";
    case "D9":
    case "D10":
      return FINISHED.has(s.final_status) ? "occurred" : s.final_status === "discarded" ? "absent" : "pending";
    default:
      return s.run.phase === "published" || s.run.phase === "paused" ? "occurred" : "pending";
  }
}

const ABSENT_WORDS: Readonly<Record<string, string>> = {
  D5: "Not applicable: no human edit",
  D6: "Not applicable: no human edit",
  D7: "Not applicable: no human edit",
  D9: "Not applicable: the action was discarded",
  D10: "Not applicable: the action was discarded",
};

const verdictWord = (v: Verdict): string => (v === "pass" ? "passed" : v === "fail" ? "failed" : v === "warn" ? "warning" : "could not tell");

/** Not applicable is a stored fact: every criterion of the result says not_applicable (nothing to check in this episode). */
const storedNotApplicable = (r: GateResult): boolean => asVerdict(r.verdict) === "unknown" && (r.criteria ?? []).length > 0 && (r.criteria ?? []).every((c) => c.result === "not_applicable");

type Verdict = "pass" | "warn" | "fail" | "unknown";
const SEVERITY: Readonly<Record<Verdict, number>> = { fail: 3, warn: 2, unknown: 1, pass: 0 };
const asVerdict = (v: string): Verdict => (v === "pass" || v === "warn" || v === "fail" ? v : "unknown");

/** The result that stands for a gate with several (the worst verdict). */
function worstOf(rs: readonly GateResult[]): GateResult {
  return [...rs].sort((a, b) => SEVERITY[asVerdict(b.verdict)] - SEVERITY[asVerdict(a.verdict)])[0]!;
}

function nodeFor(gate: string, def: GateDefinitionSource | undefined, rs: readonly GateResult[], s: EpisodeSummary, rules: EffectRules | null): PathGateNode {
  const base = { type: "gate" as const, gate, question: def?.display_question ?? def?.question ?? gate, mode: def?.mode ?? "live_required", message: def?.message ?? null };
  if (rs.length === 0) {
    const t = triggerOf(gate, s);
    if (t === "absent") return { ...base, status: "not_applicable", statusLabel: ABSENT_WORDS[gate] ?? "Not applicable in this episode", verdict: null, effect: null };
    if (t === "pending") return { ...base, status: "waiting", statusLabel: "Waiting for its trigger", verdict: null, effect: null };
    return { ...base, status: "not_recorded", statusLabel: "No result recorded", verdict: null, effect: null };
  }
  if (rs.every(storedNotApplicable)) {
    const why = (rs[0]?.criteria ?? [])[0]?.why.trim();
    return { ...base, status: "not_applicable", statusLabel: why ? `Not applicable: ${why}` : "Not applicable in this episode", verdict: null, effect: null };
  }
  const r = worstOf(rs);
  const verdict = asVerdict(r.verdict);
  const ev = controlEffectView(rs.find((x) => x.lineage?.retry_of) ?? rs.find((x) => x.lineage?.recompute_of) ?? r, def ?? null, rules);
  const effect = { headline: ev.headline, label: ev.label, hardStop: ev.hardStop };
  if (rs.some((x) => x.lineage?.retry_of)) return { ...base, status: "retried", statusLabel: `Retried: ${verdictWord(verdict)}`, verdict, effect };
  if (rs.some((x) => x.lineage?.recompute_of)) return { ...base, status: "recomputed", statusLabel: `Recomputed: ${verdictWord(verdict)}`, verdict, effect };
  if (gate === "D3" && s.human_outcome?.agreement === "overrode") return { ...base, status: "overridden", statusLabel: "Recommendation overridden by the human", verdict, effect };
  if (verdict === "pass") return { ...base, status: "passed", statusLabel: "Passed", verdict, effect };
  if (verdict === "fail") return { ...base, status: "failed", statusLabel: "Failed", verdict, effect };
  return { ...base, status: "ran", statusLabel: verdict === "warn" ? "Ran, with a warning" : "Ran, could not tell", verdict, effect };
}

export interface PathInput {
  summary: EpisodeSummary;
  results: readonly GateResult[];
  defs: readonly GateDefinitionSource[];
  rules: EffectRules | null;
  /** What the human changed, in words (the inference's statement); a generic line when it is not available. */
  editText?: string | null;
  /** The real recompute record of the edit (dependency invalidation); null when it could not be read. */
  recomputation?: DependencyInvalidation | null;
}

export function buildEpisodePath(input: PathInput): EpisodePath {
  const byGate = new Map<string, GateResult[]>();
  for (const r of input.results) byGate.set(r.gate, [...(byGate.get(r.gate) ?? []), r]);
  const defOf = new Map(input.defs.map((d) => [d.id, d]));
  const items: PathItem[] = [];
  for (const gate of PATH_ORDER) {
    // The edit comes first, then D5 reads what it meant, so no later check looks like one the edit re-derived.
    if (gate === "D5" && input.summary.human_outcome?.edited) {
      const recomputed = (input.recomputation?.entries ?? []).flatMap((e) => e.recomputed.map((r) => r.label));
      items.push({ type: "edit", text: input.editText?.trim() || "The human edited the draft before sending.", recomputed: [...new Set(recomputed)] });
    }
    items.push(nodeFor(gate, defOf.get(gate), byGate.get(gate) ?? [], input.summary, input.rules));
  }
  const gates = items.filter((i): i is PathGateNode => i.type === "gate");
  return {
    items,
    counts: {
      total: gates.length,
      ran: gates.filter((n) => n.verdict !== null).length,
      failed: gates.filter((n) => n.verdict === "fail").length,
      warned: gates.filter((n) => n.verdict === "warn").length,
      waiting: gates.filter((n) => n.status === "waiting").length,
      notApplicable: gates.filter((n) => n.status === "not_applicable").length,
    },
  };
}
