import type { CheckGroup, CheckRow as CheckRowData } from "@/lib/evals/registry";
import { WORDING } from "@/lib/evals/vocabulary";

const RuleIcon = () => (
  <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
    <path d="M4 3.5h8M4 8h8M4 12.5h5" />
  </svg>
);
const JudgeIcon = () => (
  <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round">
    <path d="M8 2.5l1.4 3.1 3.1 1.4-3.1 1.4L8 11.5 6.6 8.4 3.5 7l3.1-1.4L8 2.5Z" />
  </svg>
);
const LockIcon = () => (
  <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
    <rect x="3.5" y="7" width="9" height="6.5" rx="1.5" />
    <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
  </svg>
);

function CheckRow({ r }: { r: CheckRowData }) {
  const rule = r.grader === WORDING.graders.deterministic;
  return (
    <tr id={`eval-${r.evalType}`}>
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
      <td>
        <span className={`grader-chip${rule ? " is-rule" : ""}`}>
          {rule ? <RuleIcon /> : <JudgeIcon />}
          {r.grader}
        </span>
      </td>
      <td>
        <span className={`can-block${r.canBlock ? " is-yes" : ""}`}>
          {r.canBlock ? <LockIcon /> : null}
          {r.canBlock ? "Yes" : "No"}
        </span>
      </td>
      <td>{r.surfaces.length > 0 ? r.surfaces.join(", ") : <span className="hint">Not mapped yet</span>}</td>
    </tr>
  );
}

/** One evidence class of draft checks: what each asks, who judges it, whether it can block, and where it applies. */
export function CheckTable({ group }: { group: CheckGroup }) {
  return (
    <div className="table-scroll" role="region" aria-label={`${group.tag.tag} checks, scrollable`} tabIndex={0}>
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
          </tr>
        </thead>
        <tbody>
          {group.rows.map((r) => (
            <CheckRow key={r.evalType} r={r} />
          ))}
        </tbody>
      </table>
    </div>
  );
}
