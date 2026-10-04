import Link from "next/link";
import type { EvalBundle, EvalBundleItem, EvalResult, Knowledge } from "@/lib/api/types";
import { formatUtc } from "@/lib/format";
import { verdictCounts } from "@/lib/view/run-chain";

const VERDICT_ORDER = ["fail", "warn", "abstain", "pass", "not_relevant"] as const;

function verdictBadge(verdict: string) {
  return <span className={`badge eval-${verdict}`}>{verdict.replace("_", " ")}</span>;
}

/** One EvalResult's evidence refs (activity + quote), the chain's "show me the evidence". */
function EvidenceRefs({ result }: { result: EvalResult }) {
  if (result.evidence_refs.length === 0) return null;
  return (
    <ul className="evidence-list">
      {result.evidence_refs.map((ref, i) => (
        <li key={i} className="evidence">
          {ref.quote ? <blockquote>{ref.quote}</blockquote> : null}
          <cite>
            activity <code title={ref.activity_id}>{ref.activity_id.slice(0, 8)}</code>
            {ref.speaker_person_id ? (
              <>
                {" "}
                · speaker <code title={ref.speaker_person_id}>{ref.speaker_person_id.slice(0, 8)}</code>
              </>
            ) : null}
            {ref.occurred_at ? ` · ${formatUtc(ref.occurred_at)}` : ""}
          </cite>
        </li>
      ))}
    </ul>
  );
}

function KnowledgeRefs({ ids, knowledge }: { ids: readonly string[]; knowledge: Record<string, Knowledge | null> }) {
  if (ids.length === 0) return null;
  return (
    <p className="hint">
      knowledge:{" "}
      {ids.map((id, i) => (
        <span key={id}>
          {i > 0 ? ", " : ""}
          <Link href={`/knowledge/${id}`}>
            <code>{knowledge[id]?.key ?? id.slice(0, 8)}</code>
          </Link>
        </span>
      ))}
    </p>
  );
}

/**
 * One eval item with its "why did this fail" detail. The summary line is the verdict plus the
 * result's own reason (HAR-129 §9: "specific pass / warn / fail judgments and the reasons behind
 * them"), never the routing relevance reason, which explains why the eval applies and would read
 * as a failure next to a pass. The expanded detail adds why it applies, diagnostics, state/activity/
 * knowledge refs, evidence, suggested correction and provenance (HAR-97 §3).
 */
function EvalItem({ item, knowledge }: { item: EvalBundleItem; knowledge: Record<string, Knowledge | null> }) {
  const r = item.result;
  return (
    <li className={`eval-item eval-${item.verdict}`}>
      <details>
        <summary>
          {verdictBadge(item.verdict)} <span className="summary">{item.eval_type}</span>{" "}
          <span className="hint">{r ? r.reason : item.relevance_reason}</span>
        </summary>
        {r ? (
          <div className="eval-detail">
            <p className="hint">why this eval applies: {item.relevance_reason}</p>
            <p className="hint">
              {r.eval_version} · {r.kind}
              {r.label ? ` · label ${r.label}` : ""}
              {r.blocking ? " · " : ""}
              {r.blocking ? <strong>blocking</strong> : ""}
              {r.score !== null && r.score !== undefined ? ` · score ${r.score}` : ""}
              {r.confidence !== null && r.confidence !== undefined ? ` · confidence ${r.confidence}` : ""}
            </p>
            {r.diagnostics && r.diagnostics.length > 0 ? <p className="hint">diagnostics: {r.diagnostics.join(", ")}</p> : null}
            {r.suggested_correction ? <p className="hint">suggested correction: {r.suggested_correction}</p> : null}
            {r.state_refs.length > 0 ? <p className="hint">state fields: {r.state_refs.join(", ")}</p> : null}
            {r.activity_refs && r.activity_refs.length > 0 ? (
              <p className="hint">
                activities:{" "}
                {r.activity_refs.map((a, i) => (
                  <span key={a}>
                    {i > 0 ? ", " : ""}
                    <code title={a}>{a.slice(0, 8)}</code>
                  </span>
                ))}
              </p>
            ) : null}
            <KnowledgeRefs ids={r.knowledge_refs} knowledge={knowledge} />
            <EvidenceRefs result={r} />
            <p className="hint">
              evidence class {r.evidence_class}
              {r.model ? ` · model ${r.model}` : ""} · {formatUtc(r.created_at)}
            </p>
          </div>
        ) : (
          <p className="hint">This eval was routed but judged not relevant to the candidate; no result was produced.</p>
        )}
      </details>
    </li>
  );
}

/** The eval bundle of one candidate: every verdict listed, never collapsed into a score (HAR-129 §9). */
export function EvalList({ bundle, knowledge }: { bundle: EvalBundle | null; knowledge: Record<string, Knowledge | null> }) {
  if (!bundle) return <p className="empty">No eval bundle recorded for this candidate.</p>;
  const counts = verdictCounts(bundle);
  const policy = bundle.candidate_policy ?? null;
  const items = [...bundle.items].sort(
    (a, b) => VERDICT_ORDER.indexOf(a.verdict as (typeof VERDICT_ORDER)[number]) - VERDICT_ORDER.indexOf(b.verdict as (typeof VERDICT_ORDER)[number]),
  );
  return (
    <div className="eval-bundle">
      <p className="counts">
        {counts.fail > 0 ? <span className="count removed">{counts.fail} fail</span> : null}
        {counts.warn > 0 ? <span className="count changed">{counts.warn} warn</span> : null}
        {counts.abstain > 0 ? <span className="count">{counts.abstain} abstain</span> : null}
        <span className="count added">{counts.pass} pass</span>
        {counts.notRelevant > 0 ? <span className="count">{counts.notRelevant} not relevant</span> : null}
      </p>
      {bundle.selected_eval_suite ? <p className="hint">eval suite: {bundle.selected_eval_suite}</p> : null}
      {policy ? (
        <p className={policy.status === "restricted" ? "hint warn" : "hint"}>
          transition policy: {policy.status}
          {policy.reasons.length > 0 ? ` (${policy.reasons.join(", ")})` : ""}
          {policy.requires_human_review ? " · needs human review" : ""}
        </p>
      ) : null}
      <ul className="evals">
        {items.map((item, i) => (
          <EvalItem key={`${item.eval_type}-${i}`} item={item} knowledge={knowledge} />
        ))}
      </ul>
    </div>
  );
}
