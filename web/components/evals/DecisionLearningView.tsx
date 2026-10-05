import type { BucketView, GateView } from "@/lib/evals/registry";

function GateRow({ gate }: { gate: GateView }) {
  return (
    <li className="gate-row" id={`gate-${gate.id}`}>
      <h4>{gate.name}</h4>
      <dl className="gate-facts">
        <dt>Question</dt>
        <dd>{gate.question}</dd>
        <dt>Result</dt>
        <dd className="gate-result">{gate.built ? gate.result : "Not built yet"}</dd>
        <dt>Improves or protects</dt>
        <dd>{gate.improves}</dd>
      </dl>
      {gate.evals.length > 0 ? (
        <details className="gate-evals">
          <summary>{`${gate.evals.length} ${gate.evals.length === 1 ? "eval" : "evals"} under this gate`}</summary>
          <ul>
            {gate.evals.map((e) => (
              <li key={e.name}>
                {e.name}
                <span className="hint">{` · ${e.status} · ${e.grader}${e.judges ? ` · judges ${e.judges}` : ""} · ${e.result}`}</span>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </li>
  );
}

/**
 * The evals presented as the three buckets and their gates, not as a list of codes. Each gate answers: what question did
 * we ask, what is the result (always "Not measured" here: no recorded result is held, and a missing result is never shown
 * as a pass; a gate with no eval yet reads "Not built yet"), and what does it improve or protect. Bucket 2's gates run in
 * the order of the decision flow.
 */
export function DecisionLearningView({ buckets }: { buckets: readonly BucketView[] }) {
  return (
    <section id="decision-learning" className="decision-learning" aria-labelledby="decision-learning-h">
      <h3 id="decision-learning-h">The three buckets</h3>
      {buckets.map((b) => (
        <section key={b.id} id={`bucket-${b.id}`} aria-labelledby={`bucket-${b.id}-h`}>
          <h4 id={`bucket-${b.id}-h`} className="stage-h">{`${b.order}. ${b.name}`}</h4>
          <p className="hint">{b.question}</p>
          <ol className="gate-list">
            {b.gates.map((g) => (
              <GateRow key={g.id} gate={g} />
            ))}
          </ol>
        </section>
      ))}
    </section>
  );
}
