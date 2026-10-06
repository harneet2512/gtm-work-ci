import Link from "next/link";
import type { DependencyInvalidation } from "@/lib/api/types";
import type { RunPageData } from "@/lib/load-run";
import { formatUtc, shortId } from "@/lib/format";
import { buildChain, buildWhyChanged } from "@/lib/view/run-chain";
import { buildLoop } from "@/lib/view/run-loop";
import { evidenceContext } from "@/lib/evals/evidence";
import { HumanDecisionPanel } from "./HumanDecisionPanel";
import { LoopStrip } from "./LoopStrip";
import { PlaceholderSection } from "./PlaceholderSection";
import { Recomputation } from "./Recomputation";
import { StrategyCard } from "./StrategyCard";
import { TraceSection } from "./TraceSection";
import { WhyChangedPanel } from "./WhyChangedPanel";

/**
 * The full decision chain of one agent run (WP24, HAR-122): what gtm_ai decided (three candidates +
 * evals), why (trigger, evidence, evals, knowledge), what the human changed, and what the system
 * learned (the HAR-97 placeholders, honestly empty until WP21/WP22 populate them).
 */
export function RunChain({ data, recomputation = null, recomputationUnavailable = false }: { data: RunPageData; recomputation?: DependencyInvalidation | null; recomputationUnavailable?: boolean }) {
  const { run, trace, strategies, decision, inference, knowledge, notices } = data;
  const chain = buildChain(strategies);
  const loop = buildLoop(run, trace, strategies, decision, inference);
  const why = buildWhyChanged(strategies, decision, inference);
  const set = strategies?.strategy_set ?? null;
  const chosen = chain.find((c) => c.candidate.candidate_id === decision?.selected_candidate_id)?.candidate ?? null;
  const output = run.output;
  const evidence = evidenceContext(trace, run.id, knowledge);

  return (
    <>
      {notices.length > 0 ? (
        <ul className="notices" role="status">
          {notices.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}
      <LoopStrip phases={loop} />
      <section aria-labelledby="run-h" className="panel">
        <h2 id="run-h">Run</h2>
        <dl className="kv">
          <dt>Id</dt>
          <dd>
            <code>{run.id}</code>
          </dd>
          <dt>Workflow</dt>
          <dd>{run.workflow}</dd>
          <dt>Status</dt>
          <dd>
            <span className={`badge run-${run.status}`}>{run.status}</span>
            {run.generation ? (
              <span className={`badge gen-${run.generation.phase}`}> {run.generation.phase}</span>
            ) : null}
            {run.generation?.reason ? <span className="hint"> {run.generation.reason}</span> : null}
          </dd>
          <dt>Mode</dt>
          <dd>{run.run_mode}</dd>
          <dt>Account</dt>
          <dd>
            <Link href={`/accounts/${run.account_id}`}>
              <code>{shortId(run.account_id)}</code>
            </Link>
          </dd>
          <dt>Created</dt>
          <dd>{formatUtc(run.created_at)}</dd>
          {output?.proposed_action_type ? (
            <>
              <dt>Draft action</dt>
              <dd>
                <code>{output.proposed_action_type}</code>
                {output.reason ? <span className="hint"> — {output.reason}</span> : null}
              </dd>
            </>
          ) : null}
          {run.error ? (
            <>
              <dt>Error</dt>
              <dd>{run.error}</dd>
            </>
          ) : null}
        </dl>
      </section>
      {trace ? <TraceSection trace={trace} /> : null}
      <section aria-labelledby="candidates-h" className="panel">
        <h2 id="candidates-h">Candidates gtm_ai evaluated</h2>
        {set?.no_acceptable_candidate ? <p className="hint warn">Every candidate is blocked or restricted — gtm_ai recommends none; the human decides unaided.</p> : null}
        {chain.length === 0 ? (
          <p className="empty">No strategy set recorded for this run — either generation is still in flight or the run predates the demo path.</p>
        ) : (
          <div className="candidates">
            {chain.map((c) => (
              <StrategyCard key={c.candidate.candidate_id} chain={c} decision={decision} knowledge={knowledge} evidence={evidence} />
            ))}
          </div>
        )}
      </section>
      <WhyChangedPanel why={why} knowledge={knowledge} />
      <HumanDecisionPanel decision={decision} decisions={trace?.decisions ?? []} chosen={chosen} />
      <Recomputation recomputation={recomputation} unavailable={recomputationUnavailable} />
      <PlaceholderSection
        id="eval-runs"
        title="Eval runs"
        items={trace?.placeholders.eval_runs}
        empty="No eval runs recorded here yet. Eval results live in the candidate bundles above."
      />
      <PlaceholderSection
        id="customer-reactions"
        title="Customer reaction"
        items={trace?.placeholders.customer_reactions}
        empty="No customer reaction recorded yet. The chain ends at the send."
      />
      <PlaceholderSection
        id="knowledge-updates"
        title="What the system learned"
        items={trace?.placeholders.knowledge_updates}
        empty="No knowledge update recorded yet."
      />
    </>
  );
}
