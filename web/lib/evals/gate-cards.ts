// The gate cards: a map of gtm_ai's own causal chain, not a list of scorers. Each card says where in the chain its gate
// judges, the demo moment it fires on, and the latest real result (observed line and one evidence summary with its source)
// from the stored gate results; with no result it says when the gate runs. Definitions come from the registry (HAR-97 v2).
// A verdict criterion the definitions do not give is absent, never invented; no aggregate score exists.
import { bucketOfGate, type BucketKey, type GateRow, type GateVerdict } from "./gate-table";

export interface GateDefinitionSource {
  id: string;
  bucket: string;
  order: number;
  name: string;
  question: string;
  improves: string;
  display_name?: string;
  display_question?: string;
  invariant?: string;
  judges?: string;
  grader?: string;
  criteria?: Partial<Record<"pass" | "warn" | "fail" | "unknown", string>>;
  criteria_note?: string;
  protects?: string;
  when?: string;
  status_note?: string;
  mode?: string;
  trigger?: string;
  not_triggered?: string;
}

export interface BucketSourceLite {
  id: string;
  order: number;
  name: string;
  question: string;
}

export type CardGrader = "deterministic" | "model" | "hybrid";
export type CriterionVerdict = "pass" | "warn" | "fail" | "unknown";
export const CRITERION_ORDER: readonly CriterionVerdict[] = ["pass", "warn", "fail", "unknown"];

/** gtm_ai's causal chain, in order: the rail every card shows (the trace phases, with Precedents and Execution kept apart). */
export const CHAIN = ["Event", "Evidence", "State", "Precedents", "Knowledge", "Candidates", "Ranking", "Cliff", "Human action", "Execution", "Recompute", "Learning"] as const;
export type ChainStep = (typeof CHAIN)[number];

/**
 * The chain step(s) each gate judges, from the "at the X step" of its Judges line (B2 judges at Resolution, which is part of the
 * Evidence phase of the trace; D8 at the pre-send step and D6 against pre-send verdicts, both marked at Human action as the
 * nearest step; D9 at Execution). S gates judge the machinery, not a step.
 */
export const GATE_STEPS: Readonly<Record<string, readonly ChainStep[]>> = {
  B1: ["Evidence"],
  B2: ["Evidence"],
  B3: ["State"],
  B4: ["State"],
  B5: ["Precedents"],
  B6: ["Precedents"],
  B7: ["Knowledge"],
  B8: ["State"],
  B9: ["Learning"],
  D1: ["Candidates"],
  D2: ["Candidates"],
  D3: ["Ranking"],
  D4: ["Human action"],
  D5: ["Human action"],
  D6: ["Human action"],
  D7: ["Recompute"],
  D8: ["Human action"],
  D9: ["Execution"],
  D10: ["Learning"],
};

export interface Moment {
  label: string;
  /** True for the Slack/Cliff moments, which get the Cliff glyph. */
  cliff: boolean;
}

/** The registry's `when` in the walkthrough's words; nothing is dropped ("After M3" stays "After Cliff M3"). */
const MOMENT_WORDS: Readonly<Record<string, Moment>> = {
  "Play (M2)": { label: "Play · Cliff M2", cliff: true },
  "Choose (Slack)": { label: "Choose (Slack)", cliff: true },
  "Edit/Send, shown in M3": { label: "Edit / Send, shown in Cliff M3", cliff: true },
  "After M3": { label: "After Cliff M3", cliff: true },
  "After edit": { label: "After edit", cliff: false },
  Send: { label: "Send", cliff: false },
  "After send": { label: "After send", cliff: false },
};

/** The walkthrough's words for when a gate fires. Bucket 1 gates run when the event is published, which is Play. */
export function momentOf(id: string, when: string | undefined): Moment | null {
  if (when) return MOMENT_WORDS[when] ?? { label: when, cliff: false };
  return id.startsWith("B") ? { label: "Play", cliff: false } : null;
}

/** What to say when a gate has no result yet: never invented text, only when it runs. */
export function runsHint(moment: Moment | null): string {
  if (!moment) return "not measured";
  return moment.label === "Play" ? "Runs when you press Play" : `Runs at ${moment.label}`;
}

export type ModeKey = "live_required" | "live_conditional" | "offline_benchmark" | "continuous_aggregate";

/** HAR-97's four execution modes: the badge word and the demo-facing one-liner (gtm_ai, not the old name). */
export const MODES: Readonly<Record<ModeKey, { badge: string; line: string }>> = {
  live_required: { badge: "Live", line: "Runs on this real episode now and can affect what gtm_ai does next." },
  live_conditional: { badge: "Conditional", line: "Runs live only when this situation exists, such as an edit, precedent, send, or response." },
  offline_benchmark: { badge: "Offline", line: "Runs on a fixed set of test cases to prove the system or grader is reliable; it does not run on each customer request." },
  continuous_aggregate: { badge: "Continuous", line: "Measures traces/latency/cost/reliability across real runs over time rather than judging one decision." },
};

export const modeOf = (raw: string | undefined): ModeKey => (raw && raw in MODES ? (raw as ModeKey) : "live_required");

export interface CardExample {
  verdict: GateVerdict;
  observed: string;
  /** The recorded summary of the evidence record (not a verbatim quote); null when it could not be read. */
  summary: string | null;
  source: string | null;
  episode: string;
}

export interface GateCard {
  id: string;
  bucket: BucketKey;
  name: string;
  question: string;
  invariant: string | null;
  judges: string | null;
  steps: readonly ChainStep[];
  moment: Moment | null;
  mode: ModeKey;
  trigger: string | null;
  /** For a conditional gate: the situation whose absence reads "Not triggered". */
  notTriggered: string | null;
  grader: CardGrader | null;
  /** True for model and hybrid gates: no model grader is human-calibrated yet. */
  notCalibrated: boolean;
  criteria: Record<CriterionVerdict, string | null>;
  criteriaNote: string | null;
  protects: string | null;
  statusNote: string | null;
  /** The latest real result, preferring the MedTech episode; null when the gate has none (then `runsHint`). */
  example: CardExample | null;
}

export interface BucketCards {
  bucket: BucketKey;
  label: string;
  question: string;
  cards: GateCard[];
}

const BUCKET_OF: Readonly<Record<string, BucketKey>> = { context_intelligence: "context", decision_action: "decision", system_health: "system" };
const BUCKET_LABEL: Readonly<Record<BucketKey, string>> = { context: "Context", decision: "Decision", system: "System" };

/** "model (set + per candidate)" is a model grader; anything unrecognised is unstated, not guessed. */
export function normalizeGrader(raw: string | undefined): CardGrader | null {
  const v = (raw ?? "").trim().toLowerCase();
  if (v.startsWith("deterministic")) return "deterministic";
  if (v.startsWith("hybrid")) return "hybrid";
  if (v.startsWith("model")) return "model";
  return null;
}

const SEVERITY: Readonly<Record<GateVerdict, number>> = { fail: 0, warn: 1, unknown: 2, pass: 3 };

/** The account whose results the cards lead with when it has one (the demo case); a prop on `buildGateCards`, not a rule. */
export const PREFERRED_EPISODE_LABEL = "MedTech";

/**
 * The result a card shows for a gate: within one episode (the preferred one if it has a result) the WORST verdict, fail then warn
 * then unknown then pass, ties by the most recent judged span. A gate never reads PASS while one of its results failed.
 */
export function exampleFor(gate: string, rows: readonly GateRow[], preferred: string = PREFERRED_EPISODE_LABEL): CardExample | null {
  const mine = rows.filter((r) => r.gate === gate && r.measured && r.verdict !== null);
  if (mine.length === 0) return null;
  const episode = (mine.find((r) => r.episodeLabel.includes(preferred)) ?? mine[0]!).episodeId;
  const worst = mine
    .filter((r) => r.episodeId === episode)
    .sort((a, b) => SEVERITY[a.verdict!] - SEVERITY[b.verdict!] || (b.time ?? "").localeCompare(a.time ?? ""))[0]!;
  const withSummary = worst.evidence.find((e) => e.summary);
  return { verdict: worst.verdict!, observed: worst.observed ?? "", summary: withSummary?.summary ?? null, source: withSummary?.source ?? null, episode: worst.episodeLabel };
}

export function buildGateCards(gates: readonly GateDefinitionSource[], buckets: readonly BucketSourceLite[], rows: readonly GateRow[], preferred: string = PREFERRED_EPISODE_LABEL): BucketCards[] {
  return [...buckets]
    .sort((a, b) => a.order - b.order)
    .flatMap((b) => {
      const bucket = BUCKET_OF[b.id];
      if (!bucket) return [];
      const cards = gates
        .filter((g) => g.bucket === b.id && bucketOfGate(g.id) === bucket)
        .sort((x, y) => x.order - y.order)
        .map((g): GateCard => {
          const grader = normalizeGrader(g.grader);
          return {
            id: g.id,
            bucket,
            name: g.display_name ?? g.name,
            question: g.display_question ?? g.question,
            invariant: g.invariant ?? null,
            judges: g.judges ?? null,
            steps: GATE_STEPS[g.id] ?? [],
            moment: momentOf(g.id, g.when),
            mode: modeOf(g.mode),
            trigger: g.trigger ?? null,
            notTriggered: g.not_triggered ?? null,
            grader,
            notCalibrated: grader === "model" || grader === "hybrid",
            criteria: { pass: g.criteria?.pass ?? null, warn: g.criteria?.warn ?? null, fail: g.criteria?.fail ?? null, unknown: g.criteria?.unknown ?? null },
            criteriaNote: g.criteria_note ?? null,
            protects: g.protects ?? g.improves ?? null,
            statusNote: g.status_note ?? null,
            example: exampleFor(g.id, rows, preferred),
          };
        });
      return [{ bucket, label: BUCKET_LABEL[bucket], question: b.question, cards }];
    });
}

/** The definition the inspector shows beside a result. */
export interface GateDefinition {
  /** The card name ("Evidence fidelity"), so the inspector heads with it and not only an id. */
  name: string;
  trigger: string | null;
  invariant: string | null;
  criteria: Record<CriterionVerdict, string | null>;
  criteriaNote: string | null;
}

export function definitionsById(gates: readonly GateDefinitionSource[]): Record<string, GateDefinition> {
  return Object.fromEntries(
    gates.map((g) => [g.id, { name: g.display_name ?? g.name, trigger: g.trigger ?? null, invariant: g.invariant ?? null, criteria: { pass: g.criteria?.pass ?? null, warn: g.criteria?.warn ?? null, fail: g.criteria?.fail ?? null, unknown: g.criteria?.unknown ?? null }, criteriaNote: g.criteria_note ?? null }]),
  );
}

/** `?gate=D2` on the results table: a known gate id narrows the table to it; anything else is ignored. */
export function gateFromParam(raw: string | undefined, known: readonly string[]): string | null {
  const v = raw?.trim().toUpperCase();
  return v && known.includes(v) ? v : null;
}
