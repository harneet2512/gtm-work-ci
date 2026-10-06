// The Judgment Episode hero (HAR-145): one block that follows the three Cliff messages. Every value is read from the episode
// summary, Message 1's update, the strategy set, the human decision, the judgment inference, the recomputation, the knowledge
// mutations and the D6 gate rows. A missing piece reads "Not applicable — <reason>" (it can never happen here) or "Not run yet"
// (it has not happened); nothing is fabricated. Wording rules: a candidate rule is never called learned; a human choice is never
// called correct; Cliff is the action surface, not the judgment engine.
import type { BusinessIntelligence, DependencyInvalidation, EpisodeSummary, HumanStrategyDecision, JudgmentInference, KnowledgeMutation, RunStrategies } from "@/lib/api/types";
import type { GateRow } from "@/lib/evals/gate-table";
import { gapLabel, NAMING } from "@/lib/evals/naming";
import { compactDiff, MAX_DIFF_CHARS, type CompactDiff } from "./edit-diff";

export const DEMO_HINT = "gtm_ai read the event, recommended an action, the human exercised judgment, and what differed became a candidate rule for next time.";

export interface HeroInput {
  summary: Pick<EpisodeSummary, "triggering_event" | "final_status" | "selected_action">;
  bi: Pick<BusinessIntelligence, "summary"> | null;
  strategies: RunStrategies | null;
  decision: HumanStrategyDecision | null;
  inference: JudgmentInference | null;
  recomputation: DependencyInvalidation | null;
  mutations: KnowledgeMutation[] | null;
  gateRows: readonly Pick<GateRow, "gate" | "label" | "measured">[];
}

export interface DeltaView {
  /** The edit class(es), or "Not classified yet" while there is no inference. */
  chip: string;
  /** The interpretation, clipped; null while there is none. */
  statement: string | null;
  strength: string | null;
  /** The changed words only, when the edit is text; null when it is not (then `plain` says it). */
  diff: CompactDiff | null;
  plain: string | null;
}

export interface HeroField {
  label: string;
  lines: string[];
  /** Set on the Judgment Delta field: rendered as chip, compact diff and a full-edit toggle. */
  delta?: DeltaView;
  state: "value" | "empty";
}

export interface HeroMessage {
  id: "M1" | "M2" | "M3";
  title: string;
  fields: HeroField[];
  /** Where this message's Slack rendering and its evals are. */
  links: { cliff: string; evals: string };
}

export interface Hero {
  title: string;
  messages: HeroMessage[];
  demoHint: string;
}

const value = (label: string, lines: string[]): HeroField => ({ label, lines, state: "value" });
const empty = (label: string, line: string): HeroField => ({ label, lines: [line], state: "empty" });
const notApplicable = (label: string, reason: string) => empty(label, `Not applicable — ${reason}`);
const notRun = (label: string, why?: string) => empty(label, why ? `Not run yet — ${why}` : "Not run yet");

type Candidate = { candidate_id: string; ranking: number; title: string; preferred_by_agent: boolean };
const LETTERS = ["A", "B", "C", "D", "E"];

/** The candidates in ranking order, lettered A (the recommendation) onward. Letters are display only. */
function lettered(strategies: RunStrategies | null): { letter: string; c: Candidate }[] {
  const cands = [...((strategies?.strategy_set.candidates ?? []) as Candidate[])].sort((a, b) => a.ranking - b.ranking);
  return cands.map((c, i) => ({ letter: LETTERS[i] ?? String(i + 1), c }));
}

function recommendation(input: HeroInput): HeroField {
  const all = lettered(input.strategies);
  if (all.length === 0) return notRun("gtm_ai recommendation", "no strategy set was read");
  const [first, ...rest] = all;
  const lines = [`${first!.letter} · ${first!.c.title}`];
  if (rest.length > 0) lines.push(`Alternatives: ${rest.map((r) => `${r.letter} · ${r.c.title}`).join("; ")}`);
  return value("gtm_ai recommendation", lines);
}

function humanJudgment(input: HeroInput): HeroField {
  const label = NAMING.humanSelection;
  const d = input.decision;
  if (!d) return notRun(label, "no human choice has been recorded");
  const all = lettered(input.strategies);
  const pick = all.find((x) => x.c.candidate_id === d.selected_candidate_id);
  const pref = all.find((x) => x.c.candidate_id === d.original_agent_preference);
  if (!pick || !pref) return value(label, ["A choice was recorded; the strategy set to compare it with could not be read"]);
  return value(label, [pick.c.candidate_id === pref.c.candidate_id ? `Selected ${pick.letter}, as recommended` : `Selected ${pick.letter} instead of ${pref.letter}`]);
}

const show = (v: unknown): string => (typeof v === "string" ? v : v === null || v === undefined ? "none" : JSON.stringify(v));

function judgmentDelta(input: HeroInput): HeroField {
  const label = NAMING.humanDelta;
  const d = input.decision;
  if (!d) return notRun(label);
  if (d.edits.length === 0) return notApplicable(label, "the human made no edit");
  const delta = input.inference?.inferred_semantic_delta;
  const first = d.edits[0]!;
  const text = typeof first.before === "string" && typeof first.after === "string";
  const statement = delta?.statement ? (delta.statement.length > MAX_DIFF_CHARS ? `${delta.statement.slice(0, MAX_DIFF_CHARS - 1).trimEnd()}…` : delta.statement) : null;
  const view: DeltaView = {
    chip: !delta ? "Not classified yet" : (delta.edit_class?.join(", ") || "Unclassified"),
    statement,
    strength: delta?.signal_strength ?? null,
    diff: text ? compactDiff(first.before as string, first.after as string) : null,
    plain: text ? null : `${show(first.before)} → ${show(first.after)}`,
  };
  const lines = [view.chip, ...(statement ? [statement] : []), ...(view.strength ? [`Signal strength: ${view.strength}`] : [])];
  return { label, lines, state: "value", delta: view };
}

function recompute(input: HeroInput): HeroField {
  const label = NAMING.recompute;
  const r = input.recomputation;
  if (!r || r.status === "not_decided" || r.status === "recomputation_unavailable") return notRun(label, r?.status === "recomputation_unavailable" ? "recomputation is unavailable" : undefined);
  if (r.status === "unedited" || r.status === "discarded") return notApplicable(label, "nothing was edited, so nothing was recomputed");
  const affected = new Set(r.entries.flatMap((e) => [...e.invalidated, ...e.recomputed, ...e.not_recomputed].map((x) => x.label)));
  const stable = new Set([...r.entries.flatMap((e) => e.preserved), ...r.preserved_overall].map((x) => x.label));
  return value(label, [`${label}: ${affected.size} affected · ${stable.size} stable`]);
}

const FINAL_WORD: Readonly<Record<string, string>> = { sent: "Sent", send_recorded: "Send recorded", awaiting_send: "Awaiting send", awaiting_choice: "Awaiting the human's choice", discarded: "Discarded", decided: "Decided" };

function finalAction(input: HeroInput): HeroField {
  const label = "Final action";
  const status = FINAL_WORD[input.summary.final_status] ?? input.summary.final_status.replaceAll("_", " ");
  if (!input.summary.selected_action) return notRun(label, status.toLowerCase());
  const all = lettered(input.strategies);
  const letter = all.find((x) => x.c.candidate_id === input.summary.selected_action!.candidate_id)?.letter;
  return value(label, [`${status}: ${letter ? `${letter} · ` : ""}${input.summary.selected_action.title}`]);
}

function whatChanged(input: HeroInput): HeroField {
  const label = "What changed?";
  if (input.bi) return value(label, [input.bi.summary]);
  const trigger = input.summary.triggering_event?.summary;
  return trigger ? value(label, [`Triggering event: ${trigger}`]) : notRun(label, "no update was read for this episode");
}

function evalGap(input: HeroInput): HeroField {
  const label = NAMING.evalGap;
  const labels = [...new Set(input.gateRows.filter((r) => r.gate === "D6" && r.measured).map((r) => gapLabel(r.label)).filter((l): l is string => l !== null))];
  if (labels.length > 0) return value(label, labels);
  if (input.decision && input.decision.edits.length === 0) return notApplicable(label, "no human edit to compare against the evals");
  return notRun(label);
}

const LEARNED = new Set(["supported", "confirmed"]);

function learning(input: HeroInput): HeroField {
  const label = NAMING.knowledgeMutation;
  const m = input.mutations;
  if (m === null) return notRun(label);
  const changed = m.find((x) => x.operation !== "NO_CHANGE");
  if (!changed) return notApplicable(label, "this episode did not change company knowledge");
  const head = LEARNED.has(changed.status) ? `Learned: ${changed.title}` : `${NAMING.candidateCriterion}: ${changed.title}`;
  return value(label, [head, `Scope: ${changed.scope.replaceAll("_", " ")}`, `Evidence: ${changed.evidence.kind.replaceAll("_", " ")}`, `Status: ${changed.status}`]);
}

export function buildHero(input: HeroInput): Hero {
  return {
    title: NAMING.episode,
    demoHint: DEMO_HINT,
    messages: [
      { id: "M1", title: "Message 1 — what changed", fields: [whatChanged(input)], links: { cliff: "cliff", evals: "M1" } },
      { id: "M2", title: "Message 2 — gtm_ai recommendation → human judgment → judgment delta → recompute → final action", fields: [recommendation(input), humanJudgment(input), judgmentDelta(input), recompute(input), finalAction(input)], links: { cliff: "cliff", evals: "M2" } },
      { id: "M3", title: "Message 3 — eval gap → judgment learning", fields: [evalGap(input), learning(input)], links: { cliff: "cliff", evals: "M3" } },
    ],
  };
}
