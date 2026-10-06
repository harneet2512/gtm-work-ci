// View model for the run detail page (WP24, HAR-122): the full decision chain a viewer reads —
// three candidates with their eval bundles, the human's choice and edits, the semantic delta, and
// the learning placeholders. Pure functions over contract types only; loaders do the IO.
import type {
  EvalBundle,
  EvalBundleItem,
  EvidenceRef,
  HumanStrategyDecision,
  JudgmentInference,
  Knowledge,
  LiteralChange,
  RunStrategies,
  StrategyCandidate,
} from "@/lib/api/types";

export interface VerdictCounts {
  pass: number;
  warn: number;
  fail: number;
  abstain: number;
  notRelevant: number;
}

export interface CandidateChain {
  candidate: StrategyCandidate;
  /** The bundle candidate.eval_bundle_ref points at; null when the set names none or it is missing. */
  bundle: EvalBundle | null;
  counts: VerdictCounts;
  /** Items that did not plainly pass — the ones "why did this fail" expands. */
  attention: EvalBundleItem[];
}

export interface EvalDifference {
  evalType: string;
  preferredVerdict: string;
  chosenVerdict: string;
  /** The inference's note when one exists; null when the diff was computed from the bundles. */
  note: string | null;
}

/**
 * The "why did this action change?" comparison (HAR-129 §16). When the judgment inference exists it
 * is the record of the semantic delta; without it the panel is honest and only compares what the
 * candidates cite (knowledge-application comparison) — comparisonOnly is true.
 */
export interface WhyChanged {
  /** No human decision recorded yet. */
  decided: boolean;
  /** The human chose what gtm_ai ranked first. */
  agreed: boolean;
  preferredId: string | null;
  chosenId: string | null;
  preferredTitle: string | null;
  chosenTitle: string | null;
  /** Knowledge ids each side applied (candidate.knowledge_refs), for the with/without comparison. */
  preferredKnowledge: string[];
  chosenKnowledge: string[];
  /** Eval-verdict differences between the two sides — the inference's own list when it exists. */
  evalDifferences: EvalDifference[];
  /** gtm_ai's inferred semantic delta (inference statement). */
  statement: string | null;
  semanticLabels: string[];
  /** The human's verdict on the inference and the corrected statement, when given. */
  humanVerdict: string | null;
  correctedStatement: string | null;
  humanNote: string | null;
  /** The semantic delta's own activity evidence (the inference's evidence_refs). */
  evidenceRefs: EvidenceRef[];
  /** Candidate-level differences the inference recorded (free text). */
  candidateDifferences: string[];
  /** True when the inference says no company knowledge applied at all. */
  noApplicableKnowledge: boolean | null;
  /** True when there is no inference: only the knowledge-application comparison is shown. */
  comparisonOnly: boolean;
}

export function verdictCounts(bundle: EvalBundle | null): VerdictCounts {
  const counts: VerdictCounts = { pass: 0, warn: 0, fail: 0, abstain: 0, notRelevant: 0 };
  for (const item of bundle?.items ?? []) {
    if (item.verdict === "pass") counts.pass += 1;
    else if (item.verdict === "warn") counts.warn += 1;
    else if (item.verdict === "fail") counts.fail += 1;
    else if (item.verdict === "abstain") counts.abstain += 1;
    else counts.notRelevant += 1;
  }
  return counts;
}

/** The bundle one candidate's evals were recorded in (eval_bundle_ref first, then the FK). */
export function bundleFor(candidate: StrategyCandidate, bundles: readonly EvalBundle[]): EvalBundle | null {
  if (candidate.eval_bundle_ref) {
    const byRef = bundles.find((b) => b.id === candidate.eval_bundle_ref);
    if (byRef) return byRef;
  }
  return bundles.find((b) => b.strategy_candidate_id === candidate.candidate_id) ?? null;
}

export function buildChain(strategies: RunStrategies | null): CandidateChain[] {
  const candidates = strategies?.strategy_set.candidates ?? [];
  const bundles = strategies?.eval_bundles ?? [];
  return candidates.map((candidate) => {
    const bundle = bundleFor(candidate, bundles);
    const counts = verdictCounts(bundle);
    return {
      candidate,
      bundle,
      counts,
      attention: (bundle?.items ?? []).filter((i) => i.verdict !== "pass" && i.verdict !== "not_relevant"),
    };
  });
}

/** Every knowledge id the chain cites: candidate knowledge_refs, eval-result knowledge_refs, inference refs. */
export function knowledgeIds(strategies: RunStrategies | null, inference: JudgmentInference | null): string[] {
  const ids = new Set<string>();
  for (const c of strategies?.strategy_set.candidates ?? []) {
    for (const k of c.knowledge_refs) ids.add(k);
  }
  for (const b of strategies?.eval_bundles ?? []) {
    for (const item of b.items) {
      for (const k of item.result?.knowledge_refs ?? []) ids.add(k);
    }
  }
  for (const k of inference?.evidence.knowledge_refs ?? []) ids.add(k);
  return [...ids].sort();
}

function candidateOf(strategies: RunStrategies | null, id: string | undefined | null): StrategyCandidate | null {
  if (!id) return null;
  return strategies?.strategy_set.candidates.find((c) => c.candidate_id === id) ?? null;
}

/** Verdicts that differ between two bundles for the same eval_type — the computed fallback diff. */
function bundleDifferences(preferred: EvalBundle | null, chosen: EvalBundle | null): EvalDifference[] {
  if (!preferred || !chosen) return [];
  const chosenByType = new Map(chosen.items.map((i) => [i.eval_type, i]));
  const out: EvalDifference[] = [];
  for (const item of preferred.items) {
    const other = chosenByType.get(item.eval_type);
    if (other && other.verdict !== item.verdict) {
      out.push({ evalType: item.eval_type, preferredVerdict: item.verdict, chosenVerdict: other.verdict, note: null });
    }
  }
  return out;
}

/**
 * Builds the with/without-knowledge comparison. Sources: the strategy set (preference, candidates,
 * knowledge refs, bundles), the human's strategy decision (the choice and literal edits) and, when
 * it exists, the judgment inference (semantic delta + recorded eval differences). Nothing is
 * invented: with no inference, comparisonOnly marks the panel "knowledge-application only" and no
 * counterfactual draft is rendered.
 */
export function buildWhyChanged(
  strategies: RunStrategies | null,
  decision: HumanStrategyDecision | null,
  inference: JudgmentInference | null,
): WhyChanged {
  const set = strategies?.strategy_set ?? null;
  const preferred = candidateOf(strategies, decision?.original_agent_preference) ?? set?.candidates.find((c) => c.preferred_by_agent) ?? null;
  const chosen = candidateOf(strategies, decision?.selected_candidate_id);
  const preferredBundle = preferred ? bundleFor(preferred, strategies?.eval_bundles ?? []) : null;
  const chosenBundle = chosen ? bundleFor(chosen, strategies?.eval_bundles ?? []) : null;

  const evalDifferences: EvalDifference[] =
    inference !== null
      ? inference.evidence.eval_differences.map((d) => ({
          evalType: d.eval_type,
          preferredVerdict: d.agent_preference_verdict,
          chosenVerdict: d.human_choice_verdict,
          note: d.note ?? null,
        }))
      : bundleDifferences(preferredBundle, chosenBundle);

  return {
    decided: decision !== null,
    agreed: decision !== null && preferred !== null && decision.selected_candidate_id === preferred.candidate_id,
    preferredId: preferred?.candidate_id ?? null,
    chosenId: chosen?.candidate_id ?? null,
    preferredTitle: preferred?.title ?? null,
    chosenTitle: chosen?.title ?? null,
    preferredKnowledge: [...(preferred?.knowledge_refs ?? [])].sort(),
    chosenKnowledge: [...(chosen?.knowledge_refs ?? [])].sort(),
    evalDifferences,
    statement: inference?.inferred_semantic_delta.statement ?? null,
    semanticLabels: [...(inference?.inferred_semantic_delta.semantic_labels ?? [])],
    humanVerdict: inference?.human_verdict ?? null,
    correctedStatement: inference?.corrected_statement ?? null,
    humanNote: inference?.human_note ?? null,
    candidateDifferences: [...(inference?.evidence.candidate_differences ?? [])],
    evidenceRefs: [...(inference?.evidence.evidence_refs ?? [])],
    noApplicableKnowledge: inference?.evidence.no_applicable_knowledge ?? null,
    comparisonOnly: inference === null,
  };
}

/** The literal changes of a human strategy decision, in display order (knowledge of the delta). */
export function literalEdits(decision: HumanStrategyDecision | null): LiteralChange[] {
  return [...(decision?.edits ?? [])];
}

/** One knowledge object as a short line for the citation list (title, or the id when it could not load). */
export function knowledgeLine(id: string, knowledge: Knowledge | null | undefined): { id: string; label: string; status: string | null } {
  if (!knowledge) return { id, label: `knowledge ${id.slice(0, 8)}…`, status: null };
  const key = knowledge.key ? `${knowledge.key}: ` : "";
  return { id, label: `${key}${knowledge.title}`, status: knowledge.status };
}
