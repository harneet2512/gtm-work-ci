import Link from "next/link";
import type { Matrix, MatrixCell, MatrixColumn } from "@/lib/evals/matrix";
import { WORDING } from "@/lib/evals/vocabulary";
import { Verdict } from "./Verdict";
import { VerdictIcon } from "./VerdictIcon";
import { VerdictStrip } from "./VerdictStrip";

const StarIcon = () => (
  <svg viewBox="0 0 24 24" width="12" height="12" aria-hidden="true" focusable="false">
    <path d="m12 3.5 2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.9l-5.2 2.7 1-5.8-4.3-4.1 5.9-.9Z" fill="currentColor" />
  </svg>
);

function ColumnHead({ c, runId, selected }: { c: MatrixColumn; runId: string; selected: boolean }) {
  return (
    <th scope="col" className={`option-col${selected ? " selected" : ""}`} aria-current={selected ? "true" : undefined}>
      <Link href={`/runs/${runId}/evals?candidate=${c.candidateId}#selected`} className="option-link">
        <span className="option-letter">{c.letter}</span>
        <span className="option-title">{c.title}</span>
      </Link>
      <span className="option-marks">
        {c.isGhostPick ? (
          <span className="mark ghost-pick">
            <StarIcon />
            {WORDING.phrases.ghost_pick}
          </span>
        ) : null}
        {c.isChosen ? (
          <span className="mark chosen">
            <VerdictIcon name="check-circle" />
            Chosen
          </span>
        ) : null}
      </span>
      <VerdictStrip counts={c.counts} compact />
      {c.held ? <span className="option-held">{c.held}</span> : null}
    </th>
  );
}

function CellContent({ cell }: { cell: MatrixCell }) {
  return (
    <>
      <span className="cell-verdict">
        <Verdict verdict={cell.verdict} />
        {cell.blocking ? <span className="blocks">{WORDING.phrases.blocking}</span> : null}
      </span>
      {cell.reason ? <span className="cell-reason">{cell.reason}</span> : null}
    </>
  );
}

/** A judged cell opens its card; hovering or focusing it peeks at the evidence behind the verdict. */
function Cell({ cell, selected }: { cell: MatrixCell; selected: boolean }) {
  const className = `cell v-${cell.verdict}${selected ? " selected" : ""}`;
  if (!cell.href || !cell.resultId) {
    return (
      <td className={className}>
        <span className="cell-static">
          <CellContent cell={cell} />
        </span>
      </td>
    );
  }
  const peekId = `peek-${cell.resultId}`;
  const e = cell.evidence;
  return (
    <td className={className}>
      <Link href={cell.href} className="cell-link" aria-describedby={peekId}>
        <CellContent cell={cell} />
      </Link>
      <span id={peekId} role="tooltip" className="peek">
        {e?.quote ? <span className="peek-quote">“{e.quote}”</span> : <span className="peek-quote is-empty">This verdict cites no quote.</span>}
        {e ? (
          <span className="peek-attr">
            {[e.who ?? "Speaker not recorded", e.when, e.source].filter(Boolean).join(" · ")}
          </span>
        ) : null}
      </span>
    </td>
  );
}

/**
 * Every eval Ghost ran, by option: a verdict and a one-line reason per cell, worst rows first. Ghost's pick and
 * the human's choice are marked on the sticky column heads, each with its verdicts side by side; each column
 * title opens that option's evals below. "Not checked" (the router never selected the eval for that option)
 * is drawn apart from "Not relevant".
 */
export function EvalMatrix({ matrix, runId, selectedId }: { matrix: Matrix; runId: string; selectedId: string | null }) {
  return (
    <div className="matrix-scroll" role="region" aria-label="Eval comparison, scrollable" tabIndex={0}>
      <table className="eval-matrix">
        <caption>Every eval Ghost ran, by option. Worst first; each title opens that option's evals, and each verdict its evidence.</caption>
        <thead>
          <tr>
            <th scope="col" className="eval-col">
              Eval
            </th>
            {matrix.columns.map((c) => (
              <ColumnHead key={c.candidateId} c={c} runId={runId} selected={c.candidateId === selectedId} />
            ))}
          </tr>
        </thead>
        <tbody>
          {matrix.rows.map((r) => (
            <tr key={r.evalType}>
              <th scope="row" className="eval-col">
                <span className="eval-name" title={r.question ?? undefined}>
                  {r.name}
                </span>
                {r.tag ? (
                  <span className="evidence-tag" title={r.tag.meaning}>
                    {r.tag.tag}
                  </span>
                ) : null}
              </th>
              {r.cells.map((cell) => (
                <Cell key={cell.candidateId} cell={cell} selected={cell.candidateId === selectedId} />
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
