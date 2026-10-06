import type { Bucket2Gate, Bucket2Row, RowVerdict } from "@/lib/evals/bucket2-results";

const WORD: Readonly<Record<RowVerdict, string>> = { pass: "Pass", warn: "Warn", fail: "Fail", unknown: "Unknown" };

function Row({ row }: { row: Bucket2Row }) {
  return (
    <li className="gate-result-row" data-verdict={row.verdict}>
      {row.subGate ? <h5>{row.subGate.replace(/_/g, " ")}</h5> : null}
      <dl className="gate-facts">
        <dt>Question asked</dt>
        <dd>{row.question}</dd>
        <dt>What we observed</dt>
        <dd>{row.observed}</dd>
        <dt>Verdict</dt>
        <dd className={`verdict v-${row.verdict}`}>{WORD[row.verdict]}</dd>
        <dt>Why</dt>
        <dd>{row.why}</dd>
        <dt>Trace evidence</dt>
        <dd>
          {row.traceHref ? <a href={row.traceHref}>View in the episode trace</a> : "No trace link"}
          <span className="hint">{` · ${row.evidence.length} ${row.evidence.length === 1 ? "record" : "records"} · ${row.grader}`}</span>
        </dd>
        <dt>Improves or protects</dt>
        <dd>{row.improves}</dd>
        <dt>Calibration</dt>
        <dd className="hint">{row.calibration}</dd>
      </dl>
    </li>
  );
}

/**
 * Bucket 2 in the order of the decision flow (D1 to D10). A gate with no stored result reads "Not measured"; it is never
 * shown as a pass.
 */
export function Bucket2Results({ gates }: { gates: readonly Bucket2Gate[] }) {
  return (
    <ol className="gate-list bucket2-gates">
      {gates.map((g) => (
        <li className="gate-row" id={`gate-${g.id}`} key={g.id}>
          <h4>{g.name}</h4>
          {g.rows.length === 0 ? (
            <dl className="gate-facts">
              <dt>Question</dt>
              <dd>{g.question}</dd>
              <dt>Result</dt>
              <dd className="gate-result">{g.built === false ? "Not built yet" : "Not measured"}</dd>
              <dt>Improves or protects</dt>
              <dd>{g.improves}</dd>
            </dl>
          ) : (
            <ul className="gate-results">
              {g.rows.map((r) => (
                <Row key={r.key} row={r} />
              ))}
            </ul>
          )}
        </li>
      ))}
    </ol>
  );
}
