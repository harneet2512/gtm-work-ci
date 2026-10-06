import type { Overview } from "@/lib/evals/registry";
import { JUDGE_QUALITY_CAVEAT, REFERENCE_ANSWERS } from "@/lib/evals/quality-report";
import { formatDay } from "@/lib/format";
import { ScrollTable } from "@/components/ScrollTable";

const PERCENT = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 1 });
const pct = (v: number | null) => (v === null ? "n/a" : PERCENT.format(v));
const PERCENT0 = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 0 });

/** The pooled numbers of the recorded snapshot: how often the AI judges agreed, and what a wrong verdict costs. */
function GraderStats({ source }: { source: Overview["source"] }) {
  return (
    <dl className="catalog-stats">
      <div className="stat">
        <dt>Judge agreement</dt>
        <dd>
          <span className="stat-value num">{source ? pct(source.agreement) : "Not measured"}</span>
          <span className="stat-note">{source ? `${source.judged} judgments` : "nothing recorded yet"}</span>
        </dd>
      </div>
      <div className="stat">
        <dt>False pass · false block</dt>
        <dd>
          <span className="stat-value num">{source ? `${pct(source.falsePass)} · ${pct(source.falseBlock)}` : "Not measured"}</span>
          <span className="stat-note">{source ? "a missed problem · a stopped good action" : "nothing recorded yet"}</span>
        </dd>
      </div>
    </dl>
  );
}

/**
 * Judge quality, which belongs to the System job: how often each AI judge agreed with reference answers, how often it
 * passed something it should not have (false pass) and how often it stopped a good action (false block). The numbers are
 * a recorded measurement with the date it was recorded, NOT a live reading; rule checks are tested in code and are not in it.
 */
export function JudgeQuality({ overview }: { overview: Overview }) {
  const rows = overview.groups.flatMap((g) => g.rows).filter((r) => r.quality);
  const source = overview.source;
  return (
    <section className="judge-quality quality-callout" aria-labelledby="judge-quality-h">
      <h3 id="judge-quality-h">Judge quality</h3>
      {source ? (
        <p>
          Recorded {formatDay(source.recordedAt)}: how often each AI judge agreed with {REFERENCE_ANSWERS}. This is a recorded measurement, not a live
          reading. Checks the AI judges do not decide are tested in code and are not part of it.
        </p>
      ) : (
        <p>
          Every AI judge is itself checked against {REFERENCE_ANSWERS}: how often it agrees, how often it passes something it should not (false pass) and how
          often it stops a good action (false block). Nothing has been recorded yet, so judge quality is <strong>Not measured yet</strong>. Disagreements filed with “This
          eval is wrong” on the eval pages feed it.
        </p>
      )}
      {source ? <p className="hint">{JUDGE_QUALITY_CAVEAT}</p> : null}
      <GraderStats source={source} />
      {rows.length > 0 ? (
        <ScrollTable label="Judge quality by check">
          <table className="dense judge-table">
            <thead>
              <tr>
                <th scope="col">Check</th>
                <th scope="col">Agreement</th>
                <th scope="col">False pass</th>
                <th scope="col">False block</th>
                <th scope="col">Judged</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.evalType} id={`judge-${r.evalType}`}>
                  <th scope="row">{r.name}</th>
                  <td>{PERCENT0.format(r.quality!.agreement)}</td>
                  <td>{r.quality!.falsePass === null ? "n/a" : PERCENT0.format(r.quality!.falsePass)}</td>
                  <td>{r.quality!.falseBlock === null ? "n/a" : PERCENT0.format(r.quality!.falseBlock)}</td>
                  <td title={r.quality!.report}>{r.quality!.cases}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </ScrollTable>
      ) : null}
    </section>
  );
}
