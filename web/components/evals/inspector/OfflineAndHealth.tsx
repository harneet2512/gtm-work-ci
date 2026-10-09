// The two aggregate views (HAR-149 sections 5 and 6). Neither judges one decision: the offline checks prove the system and its
// graders reliable on fixed cases (none has run), and the health view counts how the system runs. Both are visually apart from
// the per-run verdicts: no verdict pills here, only plain states and figures.
import Link from "next/link";
import type { HealthView } from "@/lib/evals/inspector/health";
import type { OfflineView } from "@/lib/evals/inspector/offline";
import { withDemo } from "@/lib/view/demo-link";

export function OfflineBody({ view }: { view: OfflineView }) {
  return (
    <>
      <p className="agg-headline" data-testid="offline-headline">{view.headline}</p>
      <ul className="agg-list">
        {view.items.map((i) => (
          <li key={i.id} className="agg-item" data-status={i.status}>
            <div className="agg-main">
              <h2>{i.question}</h2>
              <p className="agg-what">{i.measures}</p>
              {i.trigger ? <p className="agg-meta">{`Runs on: ${i.trigger}`}</p> : null}
              {i.deviation ? <p className="agg-deviation">{`Deviation from the plan: ${i.deviation}`}</p> : null}
              {i.statusNote ? <p className="agg-meta">{i.statusNote}</p> : null}
            </div>
            <div className="agg-state">
              <span className="agg-chip" data-status={i.status}>{i.statusLabel}</span>
              <span className="agg-id mono">{i.id}</span>
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}

function Pair({ title, side }: { title: string; side: { modelCalls: string; tokens: string; cost: string } }) {
  return (
    <div className="agg-pair-col">
      <h3>{title}</h3>
      <dl className="agg-facts">
        <dt>Model calls</dt>
        <dd>{side.modelCalls}</dd>
        <dt>Tokens</dt>
        <dd>{side.tokens}</dd>
        <dt>Cost</dt>
        <dd>{side.cost}</dd>
      </dl>
    </div>
  );
}

export function HealthBody({ view, demo, episodeIds }: { view: HealthView; demo: boolean; episodeIds: readonly string[] }) {
  return (
    <>
      <p className="agg-headline">{view.note}</p>
      <section className="agg-block" aria-label="Trace completeness">
        <h2>Are the traces complete?</h2>
        <p className="agg-big">{view.trace.average}</p>
        <p className="agg-meta">{`steps of the standard chain recorded per episode, on average, over ${view.trace.episodes} ${view.trace.episodes === 1 ? "trace" : "traces"}. ${view.trace.complete} ${view.trace.complete === 1 ? "is" : "are"} complete.`}</p>
        <p className="agg-meta">{view.trace.note}</p>
      </section>
      <section className="agg-block" aria-label="Totals">
        <h2>What running it takes</h2>
        <dl className="agg-facts agg-grid">
          {view.totals.map((t) => (
            <div key={t.label}>
              <dt>{t.label}</dt>
              <dd data-measured={t.value !== "not measured"}>{t.value}</dd>
            </div>
          ))}
        </dl>
      </section>
      <section className="agg-block" aria-label="Agent versus eval cost">
        <h2>The agent versus the checks that watch it</h2>
        <div className="agg-pair">
          <Pair title="The agent (drafting, options, revision)" side={view.split.agent} />
          <Pair title="The evals (judging)" side={view.split.evals} />
        </div>
      </section>
      {episodeIds.length > 0 ? (
        <p className="agg-meta">
          {"Per-episode detail: "}
          <Link href={withDemo(`/evals/episode/${episodeIds[0]}`, demo)}>open the newest episode</Link>
        </p>
      ) : null}
    </>
  );
}
