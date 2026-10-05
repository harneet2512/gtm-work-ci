import type { EvalRun } from "@/lib/api/types";
import type { ComparisonData } from "@/lib/load-eval-runs";
import { compareView, evalRunRow, type CompareView } from "@/lib/view/eval-runs";
import { formatUtc } from "@/lib/format";
import { ScrollTable } from "@/components/ScrollTable";

const MARK: Record<string, string> = { pass: "✓", warn: "!", fail: "✗", unknown: "◌", "not checked": "—" };
const CHANGE_MARK: Record<string, string> = { improved: "↑", regressed: "↓", unchanged: "=", added: "+", removed: "−", inconclusive: "?" };

const label = (r: EvalRun) => {
  const row = evalRunRow(r);
  return `${row.account} · ${formatUtc(row.evaluatedAt)} · ${r.id.slice(0, 8)}`;
};

function Picker({ runs, a, b }: { runs: EvalRun[]; a: string | null; b: string | null }) {
  return (
    <form method="get" action="/evals" className="compare-picker">
      <input type="hidden" name="view" value="compare" />
      <label>
        Run A
        <select name="a" defaultValue={a ?? ""} required>
          <option value="" disabled>
            Choose a run
          </option>
          {runs.map((r) => (
            <option key={r.id} value={r.id}>
              {label(r)}
            </option>
          ))}
        </select>
      </label>
      <label>
        Run B
        <select name="b" defaultValue={b ?? ""} required>
          <option value="" disabled>
            Choose a run
          </option>
          {runs.map((r) => (
            <option key={r.id} value={r.id}>
              {label(r)}
            </option>
          ))}
        </select>
      </label>
      <button type="submit">Compare</button>
    </form>
  );
}

function Result({ v }: { v: CompareView }) {
  return (
    <div className="compare">
      <div className="compare-heads">
        {([["Run A", v.a], ["Run B", v.b]] as const).map(([name, s]) => (
          <div key={name} className="cmp-side">
            <span className="eyebrow">{name}</span>
            <code>{s.runId.slice(0, 8)}</code>
            <span className="hint">
              {s.account} · {formatUtc(s.evaluatedAt)}
            </span>
            <span className="hint">{s.counts}</span>
          </div>
        ))}
      </div>
      <p className={`compare-overall cmp-${v.overall.change}`}>
        Whole episode: <strong>{v.overall.word}</strong> · {v.overall.counts}
      </p>
      {v.overall.explanation ? <p className="hint">{v.overall.explanation}</p> : null}
      <ScrollTable label="Run comparison">
        <table className="dense">
          <thead>
            <tr>
              <th>Eval</th>
              <th>Run A</th>
              <th>Run B</th>
              <th>Change</th>
            </tr>
          </thead>
          <tbody>
            {v.rows.map((r) => (
              <tr key={r.evalType} className={`cmp-${r.change}`}>
                <td>{r.name}</td>
                <td className="mono">
                  {MARK[r.a]} {r.a}
                  {r.aBlocking ? " · blocks send" : ""}
                </td>
                <td className="mono">
                  {MARK[r.b]} {r.b}
                  {r.bBlocking ? " · blocks send" : ""}
                </td>
                <td>
                  <span aria-hidden="true">{CHANGE_MARK[r.change]} </span>
                  {r.changeWord}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </ScrollTable>
    </div>
  );
}

const NOTICE: Record<Exclude<ComparisonData["state"], "idle" | "compared">, string> = {
  invalid: "Choose two runs to compare.",
  not_comparable: "These runs were not triggered by the same event, so they cannot be compared. A comparison is the before and after of one trigger.",
  not_found: "One of those runs does not exist.",
  unavailable: "Backend unavailable: the comparison could not be read. This is not an eval failure.",
};

/**
 * The operator's run comparison (HAR-145): run A against run B of ONE trigger, one row per eval type. It is not part of
 * the demo walkthrough or Demo mode. A refusal (different triggers, unknown run) or an outage is explained in words and is
 * never drawn as an eval failure; an inconclusive change reads "inconclusive".
 */
export function RunCompare({ data, runs, a, b }: { data: ComparisonData; runs: EvalRun[]; a: string | null; b: string | null }) {
  return (
    <div className="compare-view">
      <p className="hint">Operator view. It compares two runs triggered by the same event, for example a re-run of one trigger after a change.</p>
      {runs.length >= 2 ? <Picker runs={runs} a={a} b={b} /> : <p className="hint">Two runs with eval results are needed to compare; {runs.length} found.</p>}
      {data.state === "compared" ? <Result v={compareView(data.comparison)} /> : data.state === "idle" ? null : <p className="notices" role="status">{NOTICE[data.state]}</p>}
    </div>
  );
}
