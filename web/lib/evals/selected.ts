// Level 2 of the eval design spec (4b-3): the evals of one action, one line each. Fail, Warn, Unsure, Pass,
// with Not relevant collapsed; each line is the result's own reason (never the routing relevance reason),
// its evidence, a small evidence-class tag, and the grader only in the detail. The headline leads with the
// judgment so the rep knows in three seconds what is risky and why.
import type { EvalBundleItem, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import type { CandidateChain } from "@/lib/view/run-chain";
import { evidenceSnippets, type EvidenceContext, type EvidenceSnippet } from "./evidence";
import {
  compareVerdicts,
  canonicalVerdict,
  diagnosticPhrase,
  evalName,
  evalQuestion,
  evidenceTag,
  fill,
  graderLabel,
  heldForReview,
  verdictWording,
  type BundleVerdict,
  type Tag,
} from "./vocabulary";

export interface KnowledgeLink {
  id: string;
  label: string;
}

export interface EvalLine {
  evalType: string;
  name: string;
  question: string | null;
  verdict: Exclude<BundleVerdict, "not_relevant">;
  /** The result's own reason: what the eval found. */
  reason: string;
  blocking: boolean;
  resultId: string;
  tag: Tag;
  /** Detail only: who judged, why the eval applies here, what to change. */
  grader: string;
  whyApplies: string;
  suggestedCorrection: string | null;
  diagnostics: string[];
  stateFields: string[];
  knowledge: KnowledgeLink[];
  evidence: EvidenceSnippet[];
  judgedAt: string;
}

export interface Headline {
  verdict: BundleVerdict | null;
  title: string;
  detail: string | null;
  /** Other lines that did not pass, beyond the one in the title. */
  more: number;
}

export interface SelectedView {
  candidateId: string;
  title: string;
  isGhostPick: boolean;
  isChosen: boolean;
  chosenBy: string | null;
  held: string | null;
  headline: Headline;
  lines: EvalLine[];
  notRelevant: { name: string; why: string }[];
}

/** The action the page opens on: the human's choice, else Ghost's pick, else the first candidate. */
export function defaultCandidateId(strategies: RunStrategies | null, decision: HumanStrategyDecision | null): string | null {
  const candidates = strategies?.strategy_set.candidates ?? [];
  const chosen = candidates.find((c) => c.candidate_id === decision?.selected_candidate_id);
  return (chosen ?? candidates.find((c) => c.preferred_by_agent) ?? candidates[0])?.candidate_id ?? null;
}

const humanizeField = (field: string): string => field.replaceAll("_", " ");

function lineOf(item: EvalBundleItem, ctx: EvidenceContext): EvalLine | null {
  const r = item.result;
  if (!r || item.verdict === "not_relevant") return null;
  return {
    evalType: item.eval_type,
    name: evalName(item.eval_type),
    question: evalQuestion(item.eval_type),
    verdict: canonicalVerdict(item.verdict),
    reason: r.reason,
    blocking: r.blocking,
    resultId: r.id,
    tag: evidenceTag(r.evidence_class),
    grader: graderLabel(r.kind, r.model),
    whyApplies: item.relevance_reason,
    suggestedCorrection: r.suggested_correction ?? null,
    diagnostics: (r.diagnostics ?? []).map(diagnosticPhrase),
    stateFields: r.state_refs.map(humanizeField),
    knowledge: r.knowledge_refs.map((id) => {
      const k = ctx.knowledge[id];
      return { id, label: k ? k.title : "Company knowledge (not loaded)" };
    }),
    evidence: evidenceSnippets(r.evidence_refs, ctx),
    judgedAt: r.created_at,
  };
}

/** Worst first; a blocking failure before any other; otherwise the router's order. */
function sortLines(lines: EvalLine[]): EvalLine[] {
  return lines
    .map((line, index) => ({ line, index }))
    .sort((a, b) => compareVerdicts(a.line.verdict, b.line.verdict) || Number(b.line.blocking) - Number(a.line.blocking) || a.index - b.index)
    .map((x) => x.line);
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

export function headlineOf(lines: readonly EvalLine[]): Headline {
  const worst = lines[0];
  if (!worst) return { verdict: null, title: "No evals recorded for this option", detail: null, more: 0 };
  const open = lines.filter((l) => l.verdict !== "pass").length;
  if (worst.verdict === "pass") return { verdict: "pass", title: "Every relevant check passed", detail: `${plural(lines.length, "check")}, all pass.`, more: 0 };
  const title = worst.blocking ? fill("send_blocked", { eval: worst.name }) : `${verdictWording(worst.verdict).label}: ${worst.name}`;
  return { verdict: worst.verdict, title, detail: worst.reason, more: open - 1 };
}


export function buildSelectedView(chain: CandidateChain, decision: HumanStrategyDecision | null, ctx: EvidenceContext): SelectedView {
  const c = chain.candidate;
  const items = chain.bundle?.items ?? [];
  const lines = sortLines(items.flatMap((i) => lineOf(i, ctx) ?? []));
  const policy = chain.bundle?.candidate_policy ?? null;
  const isChosen = decision?.selected_candidate_id === c.candidate_id;
  return {
    candidateId: c.candidate_id,
    title: c.title,
    isGhostPick: c.preferred_by_agent,
    isChosen,
    chosenBy: isChosen ? (decision?.actor_label ?? null) : null,
    held: policy?.status === "restricted" ? heldForReview(policy.reasons) : null,
    headline: headlineOf(lines),
    lines,
    notRelevant: items.filter((i) => i.verdict === "not_relevant").map((i) => ({ name: evalName(i.eval_type), why: i.relevance_reason })),
  };
}
