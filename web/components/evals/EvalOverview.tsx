import type { CheckGroup, EvalQuality, Family, Overview } from "@/lib/evals/registry";

const PERCENT = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 0 });

function Quality({ quality }: { quality: EvalQuality | null }) {
  if (!quality) return <span className="not-measured">Not measured yet</span>;
  return (
    <span className="measured">
      {PERCENT.format(quality.agreement)} agree · {PERCENT.format(quality.falsePass)} false pass · {PERCENT.format(quality.falseBlock)} false block
      <span className="hint"> ({quality.cases} cases, {quality.report})</span>
    </span>
  );
}

function CheckTable({ group }: { group: CheckGroup }) {
  return (
    <table className="check-table">
      <caption>
        <span className="evidence-tag">{group.tag.tag}</span> {group.tag.meaning}
      </caption>
      <thead>
        <tr>
          <th scope="col">Check</th>
          <th scope="col">Judged by</th>
          <th scope="col">Can block send</th>
          <th scope="col">Where it applies</th>
          <th scope="col">Measured quality</th>
        </tr>
      </thead>
      <tbody>
        {group.rows.map((r) => (
          <tr key={r.evalType} id={`eval-${r.evalType}`}>
            <th scope="row">
              <span className="eval-name">{r.name}</span>
              {r.question ? <span className="question">{r.question}</span> : null}
              <details className="definition">
                <summary>Definition</summary>
                <p>{r.definition}</p>
                {r.blockingRule ? (
                  <p>
                    <strong>When it blocks:</strong> {r.blockingRule}
                  </p>
                ) : null}
              </details>
            </th>
            <td>{r.grader}</td>
            <td>{r.canBlock ? "Yes" : "No"}</td>
            <td>{r.surfaces.length > 0 ? r.surfaces.join(", ") : <span className="hint">Not mapped in the registry yet</span>}</td>
            <td>
              <Quality quality={r.quality} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const countWords = (c: Family["counts"]) =>
  [c.live > 0 ? `${c.live} live` : null, c.partial > 0 ? `${c.partial} partial` : null, c.planned > 0 ? `${c.planned} planned` : null].filter(Boolean).join(" · ");

function FamilyBlock({ family }: { family: Family }) {
  return (
    <details className="family" id={`family-${family.id}`}>
      <summary>
        <span className="family-name">{family.name}</span>
        <span className="family-kind">{family.kind}</span>
        <span className="family-counts">{countWords(family.counts)}</span>
        <span className="ref">HAR-97 {family.id}</span>
      </summary>
      <p className="invariant">{family.invariant}</p>
      <table className="strategy-table">
        <caption className="sr-only">{`${family.name}: evals, status, grader, mode and surface`}</caption>
        <thead>
          <tr>
            <th scope="col">Eval</th>
            <th scope="col">Status</th>
            <th scope="col">Judged by</th>
            <th scope="col">Mode</th>
            <th scope="col">Surface</th>
          </tr>
        </thead>
        <tbody>
          {family.evals.map((e) => (
            <tr key={e.id}>
              <th scope="row">
                {e.name} <span className="ref">{e.id}</span>
              </th>
              <td>
                <span className={`status status-${e.status.toLowerCase()}`}>{e.status}</span>
              </td>
              <td>{e.grader}</td>
              <td>{e.mode}</td>
              <td>{e.surfaces.join(", ")}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}

/**
 * The eval overview: what Ghost checks on a drafted action and the whole HAR-97 eval strategy, straight from the
 * contracts. Measured quality appears only from an eval-of-evals report; until one exists, every row says so.
 */
export function EvalOverview({ overview }: { overview: Overview }) {
  const t = overview.totals;
  return (
    <>
      <section className={`quality-callout${overview.measured ? " measured" : ""}`} aria-labelledby="quality-h">
        <h2 id="quality-h">Measured quality</h2>
        {overview.measured ? (
          <p>Agreement with human reviewers, false passes and false blocks below come from the eval-of-evals report. Checks it does not cover say so.</p>
        ) : (
          <p>
            Every eval is itself evaluated: how often it agrees with human reviewers, how often it passes something a human had to fix (false pass)
            and how often it stops a good action (false block). Those numbers come from the eval-of-evals report, which the core does not serve yet, so
            every check below reads <strong>Not measured yet</strong>. Disagreements filed with “This eval is wrong” on the eval pages feed that report.
          </p>
        )}
      </section>
      <section className="panel" aria-labelledby="checks-h">
        <h2 id="checks-h">Checks on every drafted action</h2>
        <p className="lead">
          {t.draftTypes} checks can judge a drafted action. Only the ones that apply to the account's state and to the action run, so a decision shows a
          handful of them, each with its verdict and reason.
        </p>
        {overview.groups.map((g) => (
          <CheckTable key={g.evidenceClass} group={g} />
        ))}
      </section>
      <section className="panel" aria-labelledby="strategy-h">
        <h2 id="strategy-h">The full eval strategy</h2>
        <p className="lead">
          {t.registryEvals} evals in {overview.families.length} families cover the whole loop, from reading the source evidence to cost: {t.live} live,{" "}
          {t.partial} partial, {t.planned} planned.
        </p>
        {overview.families.map((f) => (
          <FamilyBlock key={f.id} family={f} />
        ))}
      </section>
    </>
  );
}
