import Link from "next/link";
import type { Matrix } from "@/lib/evals/matrix";
import { WORDING } from "@/lib/evals/vocabulary";
import { Verdict } from "./Verdict";
import { VerdictIcon } from "./VerdictIcon";

/**
 * Every eval Ghost ran, by option: a verdict and a one-line reason per cell, worst rows first. Ghost's pick and
 * the human's choice are marked on the columns; each column title opens that option's evals below. "Not
 * checked" (the router never selected the eval for that option) is drawn apart from "Not relevant".
 */
export function EvalMatrix({ matrix, runId, selectedId }: { matrix: Matrix; runId: string; selectedId: string | null }) {
  return (
    <div className="matrix-scroll" role="region" aria-label="Eval comparison, scrollable" tabIndex={0}>
      <table className="eval-matrix">
        <caption>Every eval Ghost ran, by option. Worst first; each title opens that option's evals.</caption>
        <thead>
          <tr>
            <th scope="col" className="eval-col">
              Eval
            </th>
            {matrix.columns.map((c) => (
              <th key={c.candidateId} scope="col" className={`option-col${c.candidateId === selectedId ? " selected" : ""}`} aria-current={c.candidateId === selectedId ? "true" : undefined}>
                <Link href={`/runs/${runId}/evals?candidate=${c.candidateId}#selected`} className="option-link">
                  <span className="option-letter">{c.letter}</span>
                  <span className="option-title">{c.title}</span>
                </Link>
                <span className="option-marks">
                  {c.isGhostPick ? (
                    <span className="mark ghost-pick">
                      <svg viewBox="0 0 24 24" width="12" height="12" aria-hidden="true" focusable="false">
                        <path d="m12 3.5 2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.9l-5.2 2.7 1-5.8-4.3-4.1 5.9-.9Z" fill="currentColor" />
                      </svg>
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
                {c.held ? <span className="option-held">{c.held}</span> : null}
              </th>
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
                <td key={cell.candidateId} className={`cell v-${cell.verdict}${cell.candidateId === selectedId ? " selected" : ""}`}>
                  <Verdict verdict={cell.verdict} />
                  {cell.blocking ? <span className="blocks">{WORDING.phrases.blocking}</span> : null}
                  {cell.reason ? <p className="cell-reason">{cell.reason}</p> : null}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
