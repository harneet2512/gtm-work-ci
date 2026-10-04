import Link from "next/link";
import type { SelectedView } from "@/lib/evals/selected";
import type { CandidateChain } from "@/lib/view/run-chain";
import { fill } from "@/lib/evals/vocabulary";
import { EvalDetail } from "@/components/evals/EvalDetail";
import { EvidenceList } from "@/components/evals/EvidenceList";
import { Verdict, VerdictCounts } from "@/components/evals/Verdict";

/**
 * The eval bundle of one candidate on the run page, in the shared eval vocabulary (eval design spec 4b-3: same
 * words, order and icons as the eval page and Slack). Each line is the verdict, the eval's plain name and the
 * result's own reason; the routing reason, grader and evidence sit in the expandable detail. Never a score. The
 * full comparison, evidence and "This eval is wrong" live on the eval page it links to.
 */
export function EvalList({ chain, view, runId }: { chain: CandidateChain; view: SelectedView; runId: string }) {
  if (!chain.bundle) return <p className="empty">No eval bundle recorded for this candidate.</p>;
  return (
    <div className="eval-bundle">
      <VerdictCounts counts={chain.counts} />
      {view.held ? <p className="held">{view.held}</p> : null}
      <ul className="evals">
        {view.lines.map((line) => (
          <li key={line.resultId} className={`eval-item v-${line.verdict}`}>
            <details>
              <summary>
                <Verdict verdict={line.verdict} /> <span className="eval-name">{line.name}</span> <span className="eval-summary-reason">{line.reason}</span>
              </summary>
              <div className="eval-detail">
                <EvalDetail line={line} />
                <EvidenceList snippets={line.evidence} />
              </div>
            </details>
          </li>
        ))}
      </ul>
      {view.notRelevant.length > 0 ? (
        <details className="not-relevant">
          <summary>{fill("not_relevant_summary", { count: view.notRelevant.length })}</summary>
          <ul>
            {view.notRelevant.map((n) => (
              <li key={n.name}>
                <strong>{n.name}</strong>: {n.why}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      <p className="eval-page-link">
        <Link href={`/runs/${runId}/evals?candidate=${chain.candidate.candidate_id}#selected`}>Compare, see the evidence or dispute an eval</Link>
      </p>
    </div>
  );
}
