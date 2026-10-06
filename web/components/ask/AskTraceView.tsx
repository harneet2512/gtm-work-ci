import type { AskTrace, AskTraceStep } from "@/lib/api/types";
import { formatUtc } from "@/lib/format";

/** The cost line: dollars to four places, or the plain fact that the provider reported none. */
export function costText(usd: number | null): string {
  return usd === null ? "not reported" : `$${usd.toFixed(4)}`;
}

/** "1.2 s" from milliseconds. */
export function durationText(ms: number): string {
  return `${(ms / 1000).toFixed(1)} s`;
}

/** What a tool returned, pretty-printed; the server already bounded it to 4 KB. */
export function outputText(output: unknown): string {
  return JSON.stringify(output, null, 2) ?? "";
}

function Step({ step }: { step: AskTraceStep }) {
  return (
    <li className="panel">
      <h3>
        {step.n}. <code>{step.tool}</code>
        {step.dry_run ? " (dry run: nothing was sent or written)" : ""}
      </h3>
      <p className="summary">{step.ok ? (step.empty ? "Found nothing." : "Returned data.") : "Could not answer."}</p>
      <h4>Input</h4>
      <pre>{JSON.stringify(step.args, null, 2)}</pre>
      <h4>Output</h4>
      <pre>{outputText(step.output)}</pre>
      {(step.links ?? []).length > 0 ? (
        <ul className="eval-diffs">
          {(step.links ?? []).map((l) => (
            <li key={l.url}>
              <a href={l.url}>{l.label}</a>
            </li>
          ))}
        </ul>
      ) : null}
    </li>
  );
}

/**
 * The trace of one Ask Cliff answer: the question, the answer, every tool Cliff called with what it asked and what came
 * back, and what it cost. Nothing here is computed: it is the record core kept when the answer was made.
 */
export function AskTraceView({ trace }: { trace: AskTrace }) {
  return (
    <>
      <section aria-labelledby="ask-trace-h" className="panel">
        <h2 id="ask-trace-h">Answer</h2>
        <dl className="kv">
          <dt>Question</dt>
          <dd className="summary">{trace.question}</dd>
          <dt>Answer</dt>
          <dd className="summary">{trace.answer_markdown}</dd>
          <dt>Asked</dt>
          <dd>{formatUtc(trace.created_at)}</dd>
          <dt>Model</dt>
          <dd>{trace.model}</dd>
          <dt>Tokens</dt>
          <dd>
            {trace.tokens_in} in, {trace.tokens_out} out
          </dd>
          <dt>Cost</dt>
          <dd>{costText(trace.cost_usd)}</dd>
          <dt>Time</dt>
          <dd>
            {durationText(trace.duration_ms)}
            {trace.timed_out ? " (ran out of time)" : ""}
          </dd>
        </dl>
      </section>
      <section aria-labelledby="ask-steps-h">
        <h2 id="ask-steps-h">Tools called ({trace.steps.length})</h2>
        {trace.steps.length === 0 ? <p className="empty">Cliff answered without reading anything.</p> : <ol>{trace.steps.map((s) => <Step key={s.n} step={s} />)}</ol>}
      </section>
    </>
  );
}
