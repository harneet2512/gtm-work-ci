import type { RunTrace } from "@/lib/api/types";
import { formatUtc, shortId } from "@/lib/format";

/**
 * The causal evidence behind the run (HAR-97 §3 backward spans): the trigger evaluation with its
 * reason codes, the activities that triggered and were correlated, the state diff and signals they
 * produced, and the context packets the agent pulled. Rendered when the core materialized a trace.
 */
export function TraceSection({ trace }: { trace: RunTrace }) {
  const te = trace.trigger_evaluation;
  const diff = trace.state_diff ?? null;
  return (
    <section aria-labelledby="trace-h" className="panel">
      <h2 id="trace-h">What triggered this run</h2>
      <dl className="kv">
        <dt>Eligibility</dt>
        <dd>
          {te.eligible ? "eligible" : "not eligible"}
          {te.reason_codes.length > 0 ? ` (${te.reason_codes.join(", ")})` : ""} · {formatUtc(te.evaluated_at)}
        </dd>
        {te.explanation ? (
          <>
            <dt>Explanation</dt>
            <dd>{te.explanation}</dd>
          </>
        ) : null}
        {(te.signal_ids ?? []).length > 0 ? (
          <>
            <dt>Signals</dt>
            <dd>
              {(te.signal_ids ?? []).map((id) => (
                <code key={id} title={id} className="ref">
                  {shortId(id)}{" "}
                </code>
              ))}
            </dd>
          </>
        ) : null}
        {trace.state_before?.version !== undefined && trace.state_at_run?.version !== undefined ? (
          <>
            <dt>State</dt>
            <dd>
              version {trace.state_before.version} → {trace.state_at_run.version}
              {diff ? ` · ${diff.changes.length} changes${diff.is_material ? " (material)" : ""}` : ""}
            </dd>
          </>
        ) : null}
      </dl>
      {diff && diff.changes.length > 0 ? (
        <details>
          <summary>state diff ({diff.changes.length} changes)</summary>
          <ul className="eval-diffs">
            {diff.changes.map((c, i) => (
              <li key={i}>
                <code>{c.field}</code> {c.op}
                {c.material ? <span className="flag">material</span> : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {trace.signals.length > 0 ? (
        <>
          <h4>Signals</h4>
          <ul className="eval-diffs">
            {trace.signals.map((s) => (
              <li key={s.id}>
                <code>{s.signal_type}</code> <span className="hint">{s.rule}{s.occurred_at ? ` · ${formatUtc(s.occurred_at)}` : ""}</span>
              </li>
            ))}
          </ul>
        </>
      ) : null}
      {trace.trigger_activities.length + (trace.correlated_activities?.length ?? 0) > 0 ? (
        <>
          <h4>Activities</h4>
          <ul className="eval-diffs">
            {trace.trigger_activities.map((a) => (
              <li key={a.id}>
                <span className="flag">trigger</span> <code title={a.id}>{shortId(a.id)}</code> {a.activity_type} · {a.summary ?? a.source_object_id}{" "}
                <span className="when">{formatUtc(a.occurred_at)}</span>
              </li>
            ))}
            {(trace.correlated_activities ?? []).map((a) => (
              <li key={a.id}>
                <code title={a.id}>{shortId(a.id)}</code> {a.activity_type} · {a.summary ?? a.source_object_id} <span className="when">{formatUtc(a.occurred_at)}</span>
              </li>
            ))}
          </ul>
        </>
      ) : null}
      {trace.context_accesses.length > 0 ? (
        <details>
          <summary>context pulls ({trace.context_accesses.length})</summary>
          <ul className="eval-diffs">
            {trace.context_accesses.map((p) => (
              <li key={p.access_id}>
                <code>{p.tool}</code> · {p.bytes} bytes{p.world_as_of ? ` · as of ${p.world_as_of}` : ""}
                {p.truncated ? " · truncated" : ""}
                {(p.items ?? []).length > 0 ? (
                  <span className="hint">
                    {" "}
                    — {(p.items ?? []).map((item, i) => {
                      const id = typeof item["id"] === "string" ? (item["id"] as string) : `#${i}`;
                      return (
                        <code key={i} title={id}>
                          {shortId(id)}{" "}
                        </code>
                      );
                    })}
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}
