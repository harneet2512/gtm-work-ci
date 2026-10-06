// The eval detail drawer's model (HAR-149 section 3): nine sections in HAR-149's order, every one built from the registry, the
// episode's stored gate results and the other episodes' stored results. A section with no data says so; no number is invented.
import type { GateResult } from "@/lib/api/types";
import { type CardGrader, type GateDefinitionSource, MODES, modeOf, normalizeGrader } from "@/lib/evals/gate-cards";
import type { EvidenceItem } from "@/lib/evals/gate-table";
import { bucketLine } from "./buckets";
import { buildCriteriaView, type CriteriaView, type CriterionRow } from "./criteria";
import { controlEffectView, type EffectRules, type EffectView } from "./effects";
import { buildFailures, buildPerformance, type FailuresView, type FleetEpisode, type PerformanceView } from "./fleet";
import { type InputItem, inputFromEvidence } from "./inputs";

export const DRAWER_SECTIONS = [
  { id: "what", title: "What are we doing?" },
  { id: "why", title: "Why do we care?" },
  { id: "when", title: "When does this run?" },
  { id: "inputs", title: "What went into it?" },
  { id: "how", title: "How was the result produced?" },
  { id: "happened", title: "What happened in this run?" },
  { id: "effect", title: "What did this result do?" },
  { id: "performance", title: "How is it performing over time?" },
  { id: "failures", title: "Common failures" },
] as const;

export interface DrawerContext {
  episodeId: string;
  episodeLabel: string;
  def: GateDefinitionSource;
  /** The episode's stored results of this gate. */
  results: readonly GateResult[];
  rules: EffectRules | null;
  resolve: (ref: string) => EvidenceItem;
  /** The words for an option or other judged object by id; null when unknown. */
  titleOf: (id: string) => string | null;
  /** Inputs the gate has beyond its cited evidence (the stored ranking, the edit). */
  extraInputs: readonly InputItem[];
  fleet: readonly FleetEpisode[];
  /** The grader's own confidence, only where one exists (the edit interpretation); otherwise null. */
  confidence: number | null;
}

export interface DrawerModel {
  gate: string;
  /** The human question, the title of the drawer. */
  question: string;
  name: string;
  episodeLabel: string;
  measured: boolean;
  what: string;
  why: { text: string; invariant: string | null };
  when: { bucket: string; modeBadge: string; modeLine: string; trigger: string | null; message: string | null; moment: string | null };
  inputs: { items: InputItem[]; advancedJson: string };
  how: {
    grader: { kind: CardGrader | null; model: string | null; promptVersion: string | null; evaluator: string | null; calibrated: false };
    criteria: CriteriaView;
    latencyMs: number | null;
    modelCalls: number | null;
    tokens: number | null;
    costUsd: number | null;
  };
  happened: {
    verdict: "pass" | "warn" | "fail" | "unknown" | null;
    statement: string;
    why: string | null;
    evidence: EvidenceItem[];
    failedChecks: CriterionRow[];
    confidence: number | null;
  };
  effect: EffectView & { impact: string | null };
  performance: PerformanceView;
  failures: FailuresView;
}

type Verdict = "pass" | "warn" | "fail" | "unknown";
const SEVERITY: Readonly<Record<Verdict, number>> = { fail: 3, warn: 2, unknown: 1, pass: 0 };
const asVerdict = (v: string): Verdict => (v === "pass" || v === "warn" || v === "fail" ? v : "unknown");

const STATEMENT: Readonly<Record<Verdict, string>> = {
  pass: "Passed: every check held, on cited evidence.",
  warn: "A warning: something is weak, but nothing is clearly wrong.",
  fail: "Failed: at least one check did not hold.",
  unknown: "Could not tell: the evidence did not settle it. This is not a pass.",
};

const firstOf = <T>(xs: readonly (T | null | undefined)[]): T | null => xs.find((x): x is T => x !== null && x !== undefined) ?? null;

function uniqueRefs(rs: readonly GateResult[]): string[] {
  return [...new Set(rs.flatMap((r) => r.evidence_refs))];
}

export function buildDrawerModel(ctx: DrawerContext): DrawerModel {
  const { def, results } = ctx;
  const measured = results.length > 0;
  const worst = measured ? [...results].sort((a, b) => SEVERITY[asVerdict(b.verdict)] - SEVERITY[asVerdict(a.verdict)])[0]! : null;
  const verdict = worst ? asVerdict(worst.verdict) : null;
  const mode = modeOf(def.mode);
  const evidence = uniqueRefs(results).map(ctx.resolve);
  const criteria = buildCriteriaView(results, (r) => ctx.titleOf(r.judged_object.id));
  const kind = normalizeGrader(def.grader) ?? (worst ? (worst.grader.kind as CardGrader) : null);
  const effect = worst ? controlEffectView(results.find((r) => r.lineage?.retry_of) ?? results.find((r) => r.lineage?.recompute_of) ?? worst, def, ctx.rules) : controlEffectView({ gate: def.id, verdict: "unknown" }, def, ctx.rules);
  const failedChecks = criteria.groups.flatMap((g) => g.rows).filter((r) => r.result === "fail" || r.result === "warn");
  const model = firstOf(results.map((r) => r.grader.model));
  const promptVersion = firstOf(results.map((r) => r.grader.prompt_version));
  const sum = (key: "tokens" | "cost_usd" | "model_calls" | "latency_ms"): number | null => {
    const xs = results.flatMap((r) => (typeof r[key] === "number" ? [r[key] as number] : []));
    return xs.length === 0 ? null : xs.reduce((a, b) => a + b, 0);
  };
  return {
    gate: def.id,
    question: def.display_question ?? def.question,
    name: def.display_name ?? def.name,
    episodeLabel: ctx.episodeLabel,
    measured,
    what: def.plain_what ?? def.display_question ?? def.question,
    why: { text: def.plain_why ?? def.protects ?? def.improves, invariant: def.invariant ?? null },
    when: { bucket: bucketLine(def.id), modeBadge: MODES[mode].badge, modeLine: MODES[mode].line, trigger: def.trigger ?? null, message: def.message ?? null, moment: def.when ?? null },
    inputs: {
      items: [...ctx.extraInputs, ...evidence.map((e) => inputFromEvidence(e, ctx.titleOf))],
      advancedJson: JSON.stringify(results.length === 1 ? results[0] : results, null, 2) ?? "[]",
    },
    how: {
      grader: { kind, model, promptVersion, evaluator: firstOf(results.map((r) => r.evaluator_version)), calibrated: false },
      criteria,
      latencyMs: sum("latency_ms"),
      modelCalls: sum("model_calls"),
      tokens: sum("tokens"),
      costUsd: sum("cost_usd"),
    },
    happened: {
      verdict,
      statement: verdict ? STATEMENT[verdict] : "No result has been recorded for this check in this episode, so it has not run. It is not a pass.",
      why: worst ? worst.why : null,
      evidence,
      failedChecks,
      confidence: ctx.confidence,
    },
    effect: { ...effect, impact: def.impact ?? null },
    performance: buildPerformance(ctx.fleet, def.id, def.grader),
    failures: buildFailures(ctx.fleet, def.id),
  };
}
