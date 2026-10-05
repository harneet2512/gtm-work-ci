import Link from "next/link";
import type { HumanStrategyDecision, Knowledge } from "@/lib/api/types";
import type { EvidenceContext } from "@/lib/evals/evidence";
import { buildSelectedView } from "@/lib/evals/selected";
import type { CandidateChain } from "@/lib/view/run-chain";
import { ArtifactView } from "./ArtifactView";
import { EvalList } from "./EvalList";

/**
 * One strategy candidate card (HAR-129 §6): ranking + preference, the draft it proposes, why Ghost
 * thinks it fits (rationale, five questions, state/evidence/knowledge refs) and its eval bundle —
 * every eval listed, never collapsed into a score.
 */
export function StrategyCard({
  chain,
  decision,
  knowledge,
  evidence,
}: {
  chain: CandidateChain;
  decision: HumanStrategyDecision | null;
  knowledge: Record<string, Knowledge | null>;
  evidence: EvidenceContext;
}) {
  const c = chain.candidate;
  const chosen = decision?.selected_candidate_id === c.candidate_id;
  return (
    <article className={`panel candidate${c.preferred_by_agent ? " preferred" : ""}${chosen ? " chosen" : ""}`} data-candidate={c.candidate_id}>
      <header className="candidate-head">
        <span className="ep-pos">#{c.ranking}</span>
        <h3>{c.title}</h3>
        {c.preferred_by_agent ? <span className="badge kind-material">Ghost's pick</span> : null}
        {chosen ? <span className="badge kind-coalesced">chosen by human</span> : null}
      </header>
      <p className="summary">{c.description}</p>
      <p className="hint">
        {c.action_type} · {c.action_class} · draft {c.draft_index ?? "—"}
        {c.selected_eval_suite ? ` · suite ${c.selected_eval_suite}` : ""}
      </p>
      <p className="hint">rationale: {c.rationale}</p>
      <details className="five-q">
        <summary>Five questions</summary>
        <dl className="kv">
          <dt>What changed</dt>
          <dd>{c.five_questions.what_changed}</dd>
          <dt>Why the state changed</dt>
          <dd>{c.five_questions.why_state_changed}</dd>
          <dt>What remains unknown</dt>
          <dd>{c.five_questions.what_remains_unknown}</dd>
          <dt>Prior knowledge</dt>
          <dd>{c.five_questions.prior_knowledge_applies}</dd>
          <dt>Why this next action</dt>
          <dd>{c.five_questions.why_next_action}</dd>
        </dl>
      </details>
      <ArtifactView artifact={c.full_action_artifact} to={c.to} cc={c.cc} heading="Proposed action" />
      {c.preview ? <p className="hint">preview: {c.preview}</p> : null}
      {c.state_refs.length > 0 ? <p className="hint">state fields: {c.state_refs.join(", ")}</p> : null}
      {c.knowledge_refs.length > 0 ? (
        <p className="hint">
          knowledge applied:{" "}
          {c.knowledge_refs.map((id, i) => (
            <span key={id}>
              {i > 0 ? ", " : ""}
              <Link href={`/knowledge/${id}`}>
                <code>{knowledge[id]?.key ?? id.slice(0, 8)}</code>
              </Link>
            </span>
          ))}
        </p>
      ) : null}
      {c.evidence_refs.length > 0 ? (
        <details className="evidence-block">
          <summary>evidence ({c.evidence_refs.length})</summary>
          <ul className="evidence-list">
            {c.evidence_refs.map((ref, i) => (
              <li key={i} className="evidence">
                {ref.quote ? <blockquote>{ref.quote}</blockquote> : null}
                <cite>
                  activity <code title={ref.activity_id}>{ref.activity_id.slice(0, 8)}</code>
                  {ref.occurred_at ? ` · ${ref.occurred_at}` : ""}
                </cite>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      <h4>Evals</h4>
      <EvalList chain={chain} view={buildSelectedView(chain, decision, evidence)} runId={evidence.runId} />
    </article>
  );
}
