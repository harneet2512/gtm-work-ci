import type { SystemView } from "@/lib/evals/system-view";

const USD = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 4 });
const NUM = new Intl.NumberFormat("en-US");
const STEP_WORDS: Readonly<Record<string, string>> = {
  build_context: "Build context",
  draft: "Draft",
  crm_intent: "CRM intent",
  await_human: "Await the human",
  execute: "Execute",
};

function duration(s: number | null): string {
  if (s === null) return "not recorded";
  if (s < 90) return `${s}s`;
  return `${Math.round(s / 60)} min`;
}

/**
 * Job 3, kept secondary and collapsed: the run's engineering view. Whether the trace reconstructs the decision,
 * the model, each step's time and what judging cost, only from what the run and its bundles recorded.
 */
export function SystemPanel({ view }: { view: SystemView }) {
  const j = view.judges;
  return (
    <details className="panel system-panel run-system">
      <summary>
        <span className="eyebrow">Job 3</span>
        <span className="system-title">System</span>
        <span className="hint">Trace completeness, model, timings and judge cost. They keep the machinery honest; they are not the loop.</span>
      </summary>
      <div className="system-grid">
        <section aria-labelledby="sys-trace-h">
          <h3 id="sys-trace-h">Trace integrity</h3>
          <ul className="sys-checks">
            {view.trace.map((c) => (
              <li key={c.label} className={c.ok ? "is-ok" : "is-missing"}>
                <span className="sr-only">{c.ok ? "Recorded: " : "Missing: "}</span>
                {c.label} <span className="hint">{c.detail}</span>
              </li>
            ))}
          </ul>
        </section>
        <section aria-labelledby="sys-steps-h">
          <h3 id="sys-steps-h">Steps</h3>
          <dl className="kv">
            {view.steps.map((s) => (
              <div key={s.step} className="kv-row">
                <dt>{STEP_WORDS[s.step] ?? s.step}</dt>
                <dd>
                  {duration(s.seconds)} <span className="hint">{s.status}</span>
                </dd>
              </div>
            ))}
          </dl>
        </section>
        <section aria-labelledby="sys-judges-h">
          <h3 id="sys-judges-h">Judging</h3>
          <dl className="kv">
            <div className="kv-row">
              <dt>Model</dt>
              <dd className="mono">{view.model ?? "not recorded"}</dd>
            </div>
            <div className="kv-row">
              <dt>Verdicts</dt>
              <dd>
                {j.verdicts} ({j.rule} rule checks, {j.ai} AI judge)
              </dd>
            </div>
            <div className="kv-row">
              <dt>Judge cost</dt>
              <dd>{j.costUsd === null ? "not recorded" : USD.format(j.costUsd)}</dd>
            </div>
            <div className="kv-row">
              <dt>Judge latency</dt>
              <dd>{j.latencyMs === null ? "not recorded" : `${NUM.format(j.latencyMs)} ms total`}</dd>
            </div>
            <div className="kv-row">
              <dt>Tokens</dt>
              <dd>{j.tokens === null ? "not recorded" : NUM.format(j.tokens)}</dd>
            </div>
          </dl>
        </section>
      </div>
    </details>
  );
}
