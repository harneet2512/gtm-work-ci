import Link from "next/link";
import type { EvalLine } from "@/lib/evals/selected";

/**
 * The expanded detail of one verdict: what the rep does not need in three seconds but an operator does. Why the
 * router applied this eval, what to change, the signals and account facts it used, the company knowledge it
 * cited, and how proven the eval is. Who judged it is on the card itself (the web eval page is the expanded view).
 */
export function EvalDetail({ line }: { line: EvalLine }) {
  return (
    <dl className="eval-detail-list">
      <dt>Why this eval applies</dt>
      <dd>{line.whyApplies}</dd>
      {line.suggestedCorrection ? (
        <>
          <dt>What would fix it</dt>
          <dd>{line.suggestedCorrection}</dd>
        </>
      ) : null}
      {line.diagnostics.length > 0 ? (
        <>
          <dt>Signals</dt>
          <dd>{line.diagnostics.join(" · ")}</dd>
        </>
      ) : null}
      {line.stateFields.length > 0 ? (
        <>
          <dt>Account facts used</dt>
          <dd>{line.stateFields.join(", ")}</dd>
        </>
      ) : null}
      {line.knowledge.length > 0 ? (
        <>
          <dt>Company knowledge</dt>
          <dd>
            {line.knowledge.map((k, i) => (
              <span key={k.id}>
                {i > 0 ? ", " : ""}
                <Link href={`/knowledge/${k.id}`}>{k.label}</Link>
              </span>
            ))}
          </dd>
        </>
      ) : null}
      <dt>How proven</dt>
      <dd>
        {line.tag.tag}: {line.tag.meaning} <Link href={`/evals?view=catalog#eval-${line.evalType}`}>What this eval checks</Link>
      </dd>
    </dl>
  );
}
