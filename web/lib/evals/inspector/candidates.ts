// The three-candidate decision screen (HAR-149 section 4, the key demo surface): the three options side by side with the evidence
// and knowledge each uses, each option's own D2 verdicts per criterion (worst first) and rank, the stored D3 ranking rationale
// ("Why A won"), "recommended" on gtm_ai's pick and "chosen" on the human's. A different pick shows the D4 interpretation; an edit
// shows D5, what D7 invalidated and recomputed, and the D8 re-run. No numeric score exists in the backend, so none is shown: the
// per-criterion verdict grid is the honest form.
import type { DependencyInvalidation, EpisodeRanking, EpisodeSummary, GateResult, JudgmentInference, RunStrategies } from "@/lib/api/types";
import { buildRecomputationView, type RecomputationView } from "@/lib/view/recomputation";
import { buildCriteriaView, type CriterionRow, worstFirst } from "./criteria";
import { criterionLabel } from "./criterion-words";

type Verdict = "pass" | "warn" | "fail" | "unknown";
const asVerdict = (v: string): Verdict => (v === "pass" || v === "warn" || v === "fail" ? v : "unknown");
const LETTERS = ["A", "B", "C"] as const;

export const SCORES_NOTE = "gtm_ai reports verdicts, not scores: each option is judged on every criterion and the worst result leads. No numeric score is computed, so none is shown.";

export interface DecisionInput {
  summary: EpisodeSummary;
  strategies: RunStrategies | null;
  results: readonly GateResult[];
  ranking: EpisodeRanking | null;
  decision: unknown;
  inference: JudgmentInference | null;
  recomputation: DependencyInvalidation | null;
  /** The words for a knowledge object by id; null when it could not be read. */
  titleOf: (id: string) => string | null;
  /** The recorded summary of an activity (an email's text); null when it could not be read. */
  evidenceText: (activityId: string) => string | null;
}

export interface CandidateCard {
  id: string;
  letter: string;
  title: string;
  strategy: string;
  intent: string;
  rank: number;
  recommended: boolean;
  chosen: boolean;
  blocked: boolean;
  actionType: string;
  evidence: { kind: string; id: string; text: string | null }[];
  stateRefs: string[];
  knowledge: { id: string; title: string | null }[];
  verdicts: { overall: Verdict | null; rows: CriterionRow[]; note: string | null };
}

export interface RankingReasonView {
  higher: string;
  lower: string;
  text: string;
}

export interface WhyWon {
  heading: string;
  reasons: RankingReasonView[];
  d3: { verdict: Verdict; why: string; criteria: CriterionRow[] } | null;
  abstained: boolean;
  /** Said when the ranking rationale is not stored. */
  missing: string | null;
}

export interface GateLine {
  verdict: Verdict;
  label: string | null;
  observed: string;
  why: string;
}

export interface HumanView {
  choseDifferent: boolean;
  headline: string;
  d4: GateLine | null;
  interpretation: string | null;
  classes: string[];
  signal: string | null;
}

export interface EditView {
  d5: GateLine | null;
  d7: GateLine | null;
  d8: (GateLine & { recomputed: boolean; previousVerdict: string | null }) | null;
  recomputation: RecomputationView | null;
}

export interface DecisionScreen {
  cards: CandidateCard[];
  whyWon: WhyWon | null;
  human: HumanView | null;
  edit: EditView | null;
  scoresNote: string;
}

const humanize = (s: string): string => {
  const t = s.toLowerCase().replaceAll("_", " ").trim();
  return t ? t.charAt(0).toUpperCase() + t.slice(1) : "";
};

const line = (r: GateResult | undefined): GateLine | null => (r ? { verdict: asVerdict(r.verdict), label: r.label ? humanize(r.label) : null, observed: r.observed, why: r.why } : null);

function verdictsFor(results: readonly GateResult[], id: string): CandidateCard["verdicts"] {
  const mine = results.filter((r) => r.gate === "D2" && r.judged_object.id === id);
  if (mine.length === 0) return { overall: null, rows: [], note: "This option has not been judged: no D2 result is stored for it." };
  const view = buildCriteriaView(mine, () => null);
  const note = view.storedCriteria ? null : "This option's result was stored as one verdict; the individual criteria were not recorded for this run.";
  return { overall: view.result, rows: worstFirst(view.groups.flatMap((g) => g.rows)), note };
}

export function buildDecisionScreen(input: DecisionInput): DecisionScreen {
  const set = input.strategies?.strategy_set;
  if (!set || set.candidates.length === 0) return { cards: [], whyWon: null, human: null, edit: null, scoresNote: SCORES_NOTE };
  const chosenId = input.summary.selected_action?.candidate_id ?? null;
  const recommendedId = input.summary.recommended_action?.candidate_id ?? [...set.candidates].sort((a, b) => a.ranking - b.ranking).find((c) => c.preferred_by_agent)?.candidate_id ?? null;
  const tiers = new Map((input.ranking?.tier_inputs ?? []).map((t) => [t.candidate_id, t]));
  const ordered = [...set.candidates].sort((a, b) => a.ranking - b.ranking);
  const cards: CandidateCard[] = ordered.map((c, i) => ({
    id: c.candidate_id,
    letter: LETTERS[i] ?? String(i + 1),
    title: c.title,
    strategy: criterionLabel(c.strategy_type),
    intent: c.description,
    rank: c.ranking,
    recommended: c.candidate_id === recommendedId,
    chosen: c.candidate_id === chosenId,
    blocked: Boolean(tiers.get(c.candidate_id)?.blocking || tiers.get(c.candidate_id)?.restricted),
    actionType: humanize(c.action_type),
    evidence: c.evidence_refs.map((e) => ({ kind: "activity", id: e.activity_id, text: e.quote ?? input.evidenceText(e.activity_id) })),
    stateRefs: [...c.state_refs],
    knowledge: c.knowledge_refs.map((id) => ({ id, title: input.titleOf(id) })),
    verdicts: verdictsFor(input.results, c.candidate_id),
  }));
  const letterOf = (id: string): string => cards.find((c) => c.id === id)?.letter ?? "?";
  const winner = cards.find((c) => c.recommended) ?? cards[0]!;
  const d3 = input.results.filter((r) => r.gate === "D3");
  const d3View = d3.length > 0 ? buildCriteriaView(d3, () => null) : null;
  const whyWon: WhyWon = {
    heading: `Why ${winner.letter} won`,
    reasons: (input.ranking?.reasons ?? []).map((r) => ({ higher: letterOf(r.ranked_higher_id), lower: letterOf(r.ranked_lower_id), text: r.reason })),
    d3: d3View && d3View.result ? { verdict: d3View.result, why: d3.map((r) => r.why.trim()).filter(Boolean).map((w) => (/[.!?]$/.test(w) ? w : `${w}.`)).join(" "), criteria: worstFirst(d3View.groups.flatMap((g) => g.rows)) } : null,
    abstained: input.ranking?.abstained ?? false,
    missing: input.ranking ? null : "The ranking rationale is not stored for this episode, so no reasons are shown.",
  };
  const chosen = cards.find((c) => c.chosen);
  const outcome = input.summary.human_outcome;
  const delta = input.inference?.inferred_semantic_delta;
  const human: HumanView | null =
    chosen && outcome
      ? {
          choseDifferent: chosen.id !== recommendedId,
          headline: chosen.id !== recommendedId ? `The human chose Option ${chosen.letter}, not gtm_ai's recommendation (Option ${winner.letter}).` : `The human agreed with gtm_ai's recommendation (Option ${winner.letter}).`,
          d4: line(input.results.find((r) => r.gate === "D4")),
          interpretation: delta && !delta.unknown ? delta.statement : delta?.unknown ? "gtm_ai could not tell why the human chose this." : null,
          classes: (delta?.edit_class ?? []).map(humanize),
          signal: delta?.signal_strength ?? null,
        }
      : null;
  const d8r = input.results.find((r) => r.gate === "D8");
  const edit: EditView | null = outcome?.edited
    ? {
        d5: line(input.results.find((r) => r.gate === "D5")),
        d7: line(input.results.find((r) => r.gate === "D7")),
        d8: d8r ? { ...line(d8r)!, recomputed: Boolean(d8r.lineage?.recompute_of), previousVerdict: d8r.lineage?.previous_verdict ?? null } : null,
        recomputation: input.recomputation ? buildRecomputationView(input.recomputation) : null,
      }
    : null;
  return { cards, whyWon, human, edit, scoresNote: SCORES_NOTE };
}
