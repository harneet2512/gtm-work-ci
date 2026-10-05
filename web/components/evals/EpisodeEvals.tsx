import type { EvalPageData } from "@/lib/load-eval-page";
import { buildAfterEdit } from "@/lib/evals/after-edit";
import { choiceSummary } from "@/lib/evals/choice";
import { evidenceContext, sourceWord } from "@/lib/evals/evidence";
import { buildMatrix, type Matrix } from "@/lib/evals/matrix";
import { buildSelectedView, defaultCandidateId } from "@/lib/evals/selected";
import { sendGate } from "@/lib/evals/send-gate";
import { buildKnowledgeTrace } from "@/lib/evals/knowledge-trace";
import type { Family } from "@/lib/evals/registry";
import { buildSystemView } from "@/lib/evals/system-view";
import { buildTraceSteps } from "@/lib/evals/trace-strip";
import { buildUnderstanding } from "@/lib/evals/understanding";
import { buildChain, type CandidateChain } from "@/lib/view/run-chain";
import type { SubmitDispute } from "./DisputeForm";
import { EvalMatrix } from "./EvalMatrix";
import { EvalPageHeader } from "./EvalPageHeader";
import { IntelligenceSection } from "./IntelligenceSection";
import { KnowledgeTraceSection } from "./KnowledgeTraceSection";
import { SystemPanel } from "./SystemPanel";
import { SelectedOption } from "./SelectedOption";
import { TraceStrip } from "./TraceStrip";
import { VerdictBanner } from "./VerdictBanner";

/** "Email from Fatoumata Touré": what set the decision off, from the trace's trigger activity. */
function triggerWords(data: EvalPageData): string | null {
  const a = data.trace?.trigger_activities[0];
  if (!a) return null;
  const from = a.participants.find((p) => p.role === "from")?.display_name;
  return from ? `${sourceWord(a.activity_type)} from ${from}` : sourceWord(a.activity_type);
}

/**
 * The episode eval page body: where the decision came from and who chose what, the trace strip, the judgment
 * first, the eval x option comparison, then the evals of one option as cards (spec 4b-3 Level 2, web depth).
 * The option in view is `candidateId` when it names one of the run's candidates, else the human's choice,
 * else Ghost's pick.
 */
export function EpisodeEvals({
  data,
  candidateId,
  dispute,
  intelligenceFamilies = [],
}: {
  data: EvalPageData;
  candidateId: string | null;
  dispute: SubmitDispute;
  /** The registry's intelligence-building families (job 1), for their build status; empty when not loaded. */
  intelligenceFamilies?: readonly Family[];
}) {
  const chain = buildChain(data.strategies);
  const known = chain.some((c) => c.candidate.candidate_id === candidateId);
  const selectedId = known ? candidateId : defaultCandidateId(data.strategies, data.decision);
  const selected = chain.find((c) => c.candidate.candidate_id === selectedId) ?? null;
  const ctx = evidenceContext(data.trace, data.run.id, data.knowledge);
  const matrix = buildMatrix(chain, data.decision, ctx);

  return (
    <>
      <EvalPageHeader
        runId={data.run.id}
        account={data.trace?.state_at_run?.account_name ?? null}
        accountId={data.run.account_id ?? null}
        decidedOn={data.strategies?.strategy_set.generated_at ?? data.run.created_at}
        trigger={triggerWords(data)}
        choice={choiceSummary(matrix, data.decision)}
      />
      <TraceStrip steps={buildTraceSteps(data)} />
      {data.notices.length > 0 ? (
        <ul className="notices" role="status">
          {data.notices.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}
      <IntelligenceSection understanding={buildUnderstanding(data.trace, ctx)} families={intelligenceFamilies} />
      <div className="job-divider" id="decision">
        <p className="eyebrow">Job 2 · Decision & feedback loop</p>
        <p className="hint">How Ghost judged its next move, what the human chose, and what the choice taught it.</p>
      </div>
      {!selected ? (
        <section className="panel" aria-labelledby="no-evals-h">
          <h2 id="no-evals-h">No evals yet</h2>
          <p className="empty">No strategy set recorded for this run yet. Evals appear here once Ghost has drafted and judged its three options.</p>
        </section>
      ) : (
        <Selected data={data} matrix={matrix} chainItem={selected} ctx={ctx} dispute={dispute} />
      )}
      <KnowledgeTraceSection trace={buildKnowledgeTrace(data)} />
      <SystemPanel view={buildSystemView(data)} />
    </>
  );
}

function Selected({ data, matrix, chainItem, ctx, dispute }: { data: EvalPageData; matrix: Matrix; chainItem: CandidateChain; ctx: ReturnType<typeof evidenceContext>; dispute: SubmitDispute }) {
  const id = chainItem.candidate.candidate_id;
  const letter = matrix.columns.find((c) => c.candidateId === id)?.letter ?? "?";
  const view = buildSelectedView(chainItem, data.decision, ctx);
  const after = buildAfterEdit(chainItem, data.decision, data.reevaluation);
  return (
    <>
      <VerdictBanner view={view} letter={letter} counts={chainItem.counts} />
      <section className="panel compare-panel" aria-labelledby="compare-h">
        <div className="panel-head">
          <h2 id="compare-h">Compare the options</h2>
          <p className="hint">Hover or focus a verdict to see the words behind it.</p>
        </div>
        {data.strategies?.strategy_set.no_acceptable_candidate ? (
          <p className="hint warn">Ghost recommends none of these: every option is blocked or held. The choice is the human's.</p>
        ) : null}
        <EvalMatrix matrix={matrix} runId={data.run.id} selectedId={id} />
      </section>
      <SelectedOption
        view={view}
        letter={letter}
        counts={chainItem.counts}
        after={after}
        gate={sendGate(view, after)}
        otherChooser={!view.isChosen && data.decision ? data.decision.actor_label : null}
        dispute={dispute}
      />
    </>
  );
}
