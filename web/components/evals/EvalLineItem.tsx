import type { EvalLine } from "@/lib/evals/selected";
import { WORDING } from "@/lib/evals/vocabulary";
import { DisputeForm, type SubmitDispute } from "./DisputeForm";
import { EvalDetail } from "./EvalDetail";
import { EvidenceList } from "./EvidenceList";
import { Verdict } from "./Verdict";

/**
 * One eval of the action (spec Level 2): verdict, the eval's plain name, the reason in one sentence, a small
 * evidence-class tag; then [Evidence], the detail (grader only here) and "This eval is wrong". The id makes it
 * a deep-link target (`#result-<id>`), which Slack's evidence links use.
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
    </li>
  );
}
