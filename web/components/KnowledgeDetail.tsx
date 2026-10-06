import Link from "next/link";
import type { Knowledge } from "@/lib/api/types";
import { formatUtc } from "@/lib/format";
import { conditionText, countsLine, historyNewestFirst, statusBadge } from "@/lib/view/knowledge";

function Conditions({ list }: { list: Knowledge["situation_signature"] }) {
  return (
    <ul className="eval-diffs">
      {list.map((c, i) => (
        <li key={i}>
          <code>{c.field}</code> {conditionText(c).replace(c.field, "").trim() || c.op}
        </li>
      ))}
    </ul>
  );
}

/**
 * One knowledge object — every §18 field: what it says (guidance), when it applies (signature +
 * applicability conditions + exceptions), the evidence behind it (counts, supporting episodes,
 * counterexamples, evidence classes, evaluator users, provenance) and its lifecycle history.
 */
export function KnowledgeDetail({ k }: { k: Knowledge }) {
  const history = historyNewestFirst(k);
  return (
    <>
      <section aria-labelledby="know-h" className="panel">
        <h2 id="know-h">Knowledge</h2>
        <dl className="kv">
          <dt>Id</dt>
          <dd>
            <code>{k.id}</code>
          </dd>
          <dt>Key</dt>
          <dd>
            <code>{k.key ?? "—"}</code>
          </dd>
          <dt>Status</dt>
          <dd>
            <span className={statusBadge(k.status)}>{k.status}</span>
          </dd>
          <dt>Title</dt>
          <dd className="summary">{k.title}</dd>
          <dt>Evidence</dt>
          <dd>{countsLine(k)}</dd>
          <dt>Created</dt>
          <dd>{formatUtc(k.created_at)}</dd>
          <dt>Last validated</dt>
          <dd>{k.last_validated_at ? formatUtc(k.last_validated_at) : "never"}</dd>
        </dl>
      </section>

      <section aria-labelledby="guid-h" className="panel">
        <h2 id="guid-h">Guidance</h2>
        <p className="summary">{k.guidance.summary}</p>
        <h4>Do</h4>
        {k.guidance.do.length === 0 ? <p className="empty">—</p> : <ul className="eval-diffs">{k.guidance.do.map((d, i) => <li key={i}>{d}</li>)}</ul>}
        <h4>Don't</h4>
        {k.guidance.dont.length === 0 ? <p className="empty">—</p> : <ul className="eval-diffs">{k.guidance.dont.map((d, i) => <li key={i}>{d}</li>)}</ul>}
      </section>

      <section aria-labelledby="scope-h" className="panel">
        <h2 id="scope-h">When it applies</h2>
        <h4>Situation signature</h4>
        <Conditions list={k.situation_signature} />
        <h4>Applicability conditions</h4>
        {(k.applicability_conditions ?? []).length === 0 ? <p className="empty">None — the signature alone scopes it.</p> : <Conditions list={k.applicability_conditions ?? []} />}
        <h4>Exceptions (checked only when the signature holds)</h4>
        {k.exceptions.length === 0 ? (
          <p className="empty">No exceptions.</p>
        ) : (
          k.exceptions.map((e, i) => (
            <div key={i} className="evidence">
              <p className="summary">{e.description}</p>
              <Conditions list={e.conditions} />
            </div>
          ))
        )}
      </section>

      <section aria-labelledby="ev-h" className="panel">
        <h2 id="ev-h">Evidence</h2>
        <dl className="kv">
          <dt>Decisions</dt>
          <dd>{k.counts.decisions}</dd>
          <dt>Reactions</dt>
          <dd>
            {k.counts.positive_reactions} positive · {k.counts.negative_reactions} negative
          </dd>
          <dt>Outcomes advanced</dt>
          <dd>{k.counts.outcomes_advanced}</dd>
          <dt>Counterexamples</dt>
          <dd>{k.counts.counterexamples}</dd>
          <dt>Evidence classes</dt>
          <dd>{(k.evidence_classes ?? []).length > 0 ? (k.evidence_classes ?? []).join(", ") : "—"}</dd>
          <dt>Used by evaluators</dt>
          <dd>{(k.used_by_evaluators ?? []).length > 0 ? (k.used_by_evaluators ?? []).map((e) => <code key={e}>{e} </code>) : "—"}</dd>
        </dl>
        <h4>Supporting decision episodes</h4>
        {k.supporting_decision_episode_ids.length === 0 ? (
          <p className="empty">None yet.</p>
        ) : (
          <ul className="eval-diffs">
            {k.supporting_decision_episode_ids.map((id) => (
              <li key={id}>
                <Link href={`/episodes/${id}`}>
                  <code>{id}</code>
                </Link>
              </li>
            ))}
          </ul>
        )}
        <h4>Counterexamples</h4>
        {k.counterexamples.length === 0 ? (
          <p className="empty">None.</p>
        ) : (
          k.counterexamples.map((c, i) => (
            <div key={i} className="evidence">
              <p className="summary">{c.note}</p>
              <cite>
                episode <code>{c.decision_episode_id.slice(0, 8)}</code>
              </cite>
            </div>
          ))
        )}
        <h4>Provenance</h4>
        <dl className="kv">
          <dt>Created from</dt>
          <dd>
            <code>{k.provenance.created_from}</code>
          </dd>
          {k.provenance.source_decision_episode_id ? (
            <>
              <dt>Source episode</dt>
              <dd>
                <Link href={`/episodes/${k.provenance.source_decision_episode_id}`}>
                  <code>{k.provenance.source_decision_episode_id}</code>
                </Link>
              </dd>
            </>
          ) : null}
          {k.provenance.note ? (
            <>
              <dt>Note</dt>
              <dd>{k.provenance.note}</dd>
            </>
          ) : null}
        </dl>
      </section>

      <section aria-labelledby="hist-h" className="panel">
        <h2 id="hist-h">Lifecycle history</h2>
        {history.length === 0 ? (
          <p className="empty">No status changes recorded.</p>
        ) : (
          <ul className="eval-diffs">
            {history.map((h, i) => (
              <li key={i}>
                <code>{h.from_status ?? "∅"}</code> → <span className={statusBadge(h.to_status)}>{h.to_status}</span>{" "}
                <span className="hint">
                  {formatUtc(h.changed_at)} · {h.reason}
                  {h.evidence_kind ? ` · evidence ${h.evidence_kind}` : ""}
                  {h.evidence_ref_id ? (
                    <>
                      {" "}
                      {h.evidence_kind === "decision_episode" ? (
                        <Link href={`/episodes/${h.evidence_ref_id}`}>
                          <code title={h.evidence_ref_id}>{h.evidence_ref_id.slice(0, 8)}</code>
                        </Link>
                      ) : (
                        <code title={h.evidence_ref_id}>{h.evidence_ref_id.slice(0, 8)}</code>
                      )}
                    </>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  );
}
