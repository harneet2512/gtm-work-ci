import type { Family } from "@/lib/evals/registry";

const countWords = (c: Family["counts"]) =>
  [c.live > 0 ? `${c.live} live` : null, c.partial > 0 ? `${c.partial} partial` : null, c.planned > 0 ? `${c.planned} planned` : null].filter(Boolean).join(" · ");

/** Live, partial and planned as one thin bar; the words beside it carry the meaning. */
export function StatusBar({ counts }: { counts: Family["counts"] }) {
  const total = counts.live + counts.partial + counts.planned || 1;
  const pct = (n: number) => `${(n / total) * 100}%`;
  return (
    <span className="status-bar" aria-hidden="true">
      <span className="is-live" style={{ width: pct(counts.live) }} />
      <span className="is-partial" style={{ width: pct(counts.partial) }} />
      <span className="is-planned" style={{ width: pct(counts.planned) }} />
    </span>
  );
}

function StrategyTable({ family }: { family: Family }) {
  return (
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
  );
}

/** One eval family, collapsed: its name, kind and build status; open for its invariant and evals. */
export function FamilyBlock({ family }: { family: Family }) {
  return (
    <details className="family" id={`family-${family.id}`}>
      <summary>
        <span className="family-name">{family.name}</span>
        <span className="family-kind">{family.kind}</span>
        <StatusBar counts={family.counts} />
        <span className="family-counts">{countWords(family.counts)}</span>
        <span className="ref">{family.id}</span>
      </summary>
      <p className="invariant">{family.invariant}</p>
      <div className="table-scroll">
        <StrategyTable family={family} />
      </div>
    </details>
  );
}
