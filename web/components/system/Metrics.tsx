import type { MetricsRead } from "@/lib/load-system";
import { metricsView } from "@/lib/view/metrics";
import { ScrollTable } from "@/components/ScrollTable";

const EXPLAIN: Record<Exclude<MetricsRead["state"], "ok">, string> = {
  none_asked: "Open a demo account to read its metrics.",
  no_episode: "No decision episode has been recorded yet, so there are no metrics.",
  not_found: "No metrics are recorded for that episode.",
  unavailable: "Backend unavailable: the metrics could not be read.",
};

/**
 * What producing the latest decision cost (HAR-145), as a metric: calls, tokens, retries, cost and time. A figure nobody
 * reported reads "not measured"; a replayed run reports no spend. Metrics carry no verdict.
 */
export function Metrics({ state }: { state: MetricsRead }) {
  const v = state.state === "ok" ? metricsView(state.metrics) : null;
  return (
    <section className="card metrics" aria-label="Operational metrics">
      <h2>Operational metrics</h2>
      <p className="hint">Metrics describe cost and speed. They have no verdict.</p>
      {!v ? (
        <p className="empty">{EXPLAIN[state.state as Exclude<MetricsRead["state"], "ok">]}</p>
      ) : (
        <>
          <p className="sys-summary">{v.status}</p>
          {v.note ? <p className="hint">{v.note}</p> : null}
          <dl className="kv">
            {v.facts.map((f) => (
              <div key={f.label} className="kv-row">
                <dt>{f.label}</dt>
                <dd>{f.value}</dd>
              </div>
            ))}
          </dl>
          {v.models.length > 0 ? <p className="hint">Models: {v.models.join(", ")}</p> : null}
          {v.stages.length > 0 ? (
            <ScrollTable label="Metrics by stage">
              <table className="explorer-table">
                <thead>
                  <tr>
                    <th>Stage</th>
                    <th>Model calls</th>
                    <th>Tokens</th>
                    <th>Cost</th>
                    <th>Worker call time</th>
                  </tr>
                </thead>
                <tbody>
                  {v.stages.map((s) => (
                    <tr key={s.name}>
                      <td>{s.name}</td>
                      <td>{s.modelCalls}</td>
                      <td>{s.tokens}</td>
                      <td>{s.cost}</td>
                      <td>{s.workerTime}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </ScrollTable>
          ) : null}
        </>
      )}
    </section>
  );
}
