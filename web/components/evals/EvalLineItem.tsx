import type { EvalLine } from "@/lib/evals/selected";
import { WORDING } from "@/lib/evals/vocabulary";
import { formatDay } from "@/lib/format";
import { DisputeForm, type SubmitDispute } from "./DisputeForm";
import { EvalDetail } from "./EvalDetail";
import { EvidenceList } from "./EvidenceList";
import { Verdict } from "./Verdict";

const GraderIcon = ({ rule }: { rule: boolean }) => (
  <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    {rule ? <path d="M4 3.5h8M4 8h8M4 12.5h5" /> : <path d="M8 2.5l1.4 3.1 3.1 1.4-3.1 1.4L8 11.5 6.6 8.4 3.5 7l3.1-1.4L8 2.5Z" />}
  </svg>
);

/**
 * One eval of the action as a card (spec Level 2, web depth): the verdict, the eval's plain name, the reason in
 * one sentence, a small evidence-class tag; the evidence drawer (who, when, the exact words, the source), who
 * judged it and when, the detail, and "This eval is wrong". The id makes it a deep-link target
 * (`#result-<id>`), which Slack's evidence links and the comparison's cells use.
 */
export function EvalLineItem({ line, dispute }: { line: EvalLine; dispute: SubmitDispute }) {
  return (
    <li id={`result-${line.resultId}`} className={`eval-line v-${line.verdict}`}>
      <div className="eval-line-head">
        <Verdict verdict={line.verdict} />
        <h3 className="eval-name" title={line.question ?? undefined}>
          {line.name}
        </h3>
        {line.blocking ? <span className="blocks">{WORDING.phrases.blocking}</span> : null}
        <span className="evidence-tag" title={line.tag.meaning}>
          {line.tag.tag}
        </span>
      </div>
      <p className="eval-reason">{line.reason}</p>
      <div className="eval-line-actions">
        <details className="eval-evidence">
          <summary>
            {WORDING.phrases.evidence}
            {line.evidence.length > 0 ? ` (${line.evidence.length})` : ""}
          </summary>
          <EvidenceList snippets={line.evidence} />
        </details>
        <details className="eval-more">
          <summary>Details</summary>
          <EvalDetail line={line} />
        </details>
        <DisputeForm resultId={line.resultId} evalName={line.name} verdict={line.verdict} submit={dispute} />
      </div>
      <p className="eval-line-meta">
        <GraderIcon rule={line.grader.startsWith(WORDING.graders.deterministic)} />
        <span className="grader">{line.grader}</span>
        <span aria-hidden="true">·</span>
        <span>
          judged <time dateTime={line.judgedAt}>{formatDay(line.judgedAt)}</time>
        </span>
      </p>
    </li>
  );
}
