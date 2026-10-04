import type { EvalPageData } from "@/lib/load-eval-page";
import { buildAfterEdit } from "@/lib/evals/after-edit";
import { evidenceContext } from "@/lib/evals/evidence";
import { buildMatrix } from "@/lib/evals/matrix";
import { buildSelectedView, defaultCandidateId } from "@/lib/evals/selected";
import { WORDING, fill } from "@/lib/evals/vocabulary";
import { buildChain } from "@/lib/view/run-chain";
import { AfterEditPanel } from "./AfterEditPanel";
import type { SubmitDispute } from "./DisputeForm";
import { EvalLineItem } from "./EvalLineItem";
import { EvalMatrix } from "./EvalMatrix";
import { VerdictBanner } from "./VerdictBanner";

/**
 * The episode eval page body: the judgment first, then the eval x option comparison, then the evals of one
 * option, one line each (spec 4b-3 Level 2). The option in view is `candidateId` when it names one of the run's
 * candidates, else the human's choice, else Ghost's pick.
 */
export function EpisodeEvals({ data, candidateId, dispute }: { data: EvalPageData; candidateId: string | null; dispute: SubmitDispute }) {
  const chain = buildChain(data.strategies);
  const known = chain.some((c) => c.candidate.candidate_id === candidateId);
  const selectedId = known ? candidateId : defaultCandidateId(data.strategies, data.decision);
  const selected = chain.find((c) => c.candidate.candidate_id === selectedId) ?? null;
  const matrix = buildMatrix(chain, data.decision);
  const ctx = evidenceContext(data.trace, data.run.id, data.knowledge);

  return (
    <>
      {data.notices.length > 0 ? (
        <ul className="notices" role="status">
          {data.notices.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}
      {!selected ? (
        <section className="panel" aria-labelledby="no-evals-h">
          <h2 id="no-evals-h">No evals yet</h2>
          <p className="empty">No strategy set recorded for this run yet. Evals appear here once Ghost has drafted and judged its three options.</p>
        </section>
      ) : (
        <Selected data={data} matrix={matrix} selectedId={selected.candidate.candidate_id} view={buildSelectedView(selected, data.decision, ctx)} chainItem={selected} dispute={dispute} />
      )}
    </>
  );
}

function Selected({
  data,
  matrix,
  selectedId,
  view,
  chainItem,
  dispute,
}: {
  data: EvalPageData;
  matrix: ReturnType<typeof buildMatrix>;
  selectedId: string;
  view: ReturnType<typeof buildSelectedView>;
  chainItem: ReturnType<typeof buildChain>[number];
  dispute: SubmitDispute;
}) {
  const letter = matrix.columns.find((c) => c.candidateId === selectedId)?.letter ?? "?";
  const after = buildAfterEdit(chainItem, data.decision, data.reevaluation);
  return (
    <>
      <VerdictBanner view={view} letter={letter} />
      <section className="panel" aria-labelledby="compare-h">
        <h2 id="compare-h">Compare the options</h2>
        {data.strategies?.strategy_set.no_acceptable_candidate ? (
          <p className="hint warn">Ghost recommends none of these: every option is blocked or held. The choice is the human's.</p>
        ) : null}
        <EvalMatrix matrix={matrix} runId={data.run.id} selectedId={selectedId} />
      </section>
      <section id="selected" className="panel selected-evals" aria-labelledby="selected-h">
        <h2 id="selected-h">{`Evals for option ${letter}: ${view.title}`}</h2>
        <p className="option-marks">
          {view.isGhostPick ? <span className="mark ghost-pick">{WORDING.phrases.ghost_pick}</span> : null}
          {view.isChosen && view.chosenBy ? <span className="mark chosen">{fill("human_choice", { actor: view.chosenBy })}</span> : null}
          {!view.isChosen && data.decision ? <span className="hint">This is not the option {data.decision.actor_label} chose.</span> : null}
        </p>
        {view.held ? <p className="held">{view.held}</p> : null}
        <AfterEditPanel view={after} />
        {view.lines.length === 0 ? (
          <p className="empty">No eval returned a verdict for this option.</p>
        ) : (
          <ol className="eval-lines">
            {view.lines.map((line) => (
              <EvalLineItem key={line.resultId} line={line} dispute={dispute} />
            ))}
          </ol>
        )}
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
      </section>
    </>
  );
}
