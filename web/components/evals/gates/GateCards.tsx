import Link from "next/link";
import { CHAIN, CRITERION_ORDER, MODES, runsHint, type BucketCards, type GateCard } from "@/lib/evals/gate-cards";
import { VerdictPill } from "./VerdictPill";

/** The Slack mark for the Cliff moments: a speech bubble. Decorative; the moment's words are beside it. */
function CliffGlyph() {
  return (
    <svg className="cliff-glyph" viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round">
      <path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z" />
    </svg>
  );
}

/** Our pipeline as a thin rail; the step(s) this gate judges are marked (shape and weight, not only color). */
function ChainRail({ steps }: { steps: readonly string[] }) {
  return (
    <div className="chain">
      <ol className="chain-rail" aria-label={`Judges the ${steps.join(", ")} step of the chain`}>
        {CHAIN.map((s) => (
          <li key={s} data-on={steps.includes(s)} aria-current={steps.includes(s) ? "step" : undefined} title={s}>
            <span className="tick" aria-hidden="true" />
          </li>
        ))}
      </ol>
      <span className="rail-cap">{steps.join(" · ")}</span>
    </div>
  );
}

function Result({ c }: { c: GateCard }) {
  const e = c.example;
  if (!e) {
    // A conditional gate with no result did not trigger: that is neither a pass nor "not measured".
    if (c.mode === "live_conditional" && c.notTriggered) return <p className="card-result hint" data-state="not-triggered">{`Not triggered — ${c.notTriggered} in this episode`}</p>;
    return <p className="card-result hint">{runsHint(c.moment)}</p>;
  }
  return (
    <div className="card-result">
      <p className="result-line">
        <VerdictPill verdict={e.verdict} />
        {c.notCalibrated ? <span className="uncal-tag">not yet calibrated</span> : null}
      </p>
      <p className="result-observed">{e.observed}</p>
      <p className="result-episode hint">{e.episode}</p>
      {e.summary ? (
        <p className="result-summary">
          <span className="shape-activity" aria-hidden="true" />
          <span>{e.summary}</span>
          <cite>{`Summary${e.source ? ` · ${e.source}` : ""}`}</cite>
        </p>
      ) : null}
    </div>
  );
}

function Details({ c }: { c: GateCard }) {
  const given = CRITERION_ORDER.filter((v) => c.criteria[v] !== null);
  if (!c.invariant && !c.trigger && given.length === 0 && !c.criteriaNote) return null;
  return (
    <details className="card-details">
      <summary>Details</summary>
      {c.invariant ? <p><strong>Catches:</strong> {c.invariant}</p> : null}
      {given.length > 0 ? (
        <ul className="card-criteria" aria-label={`${c.id} verdict criteria`}>
          {given.map((v) => (
            <li key={v}>
              <VerdictPill verdict={v} />
              <span>{c.criteria[v]}</span>
            </li>
          ))}
        </ul>
      ) : null}
      {c.criteriaNote ? <p className="hint">{c.criteriaNote}</p> : null}
      {c.trigger ? <p><strong>Runs:</strong> {c.trigger}</p> : null}
      <p className="hint">
        {c.grader ? `Grader: ${c.grader}` : ""}
        {c.notCalibrated ? " · not yet calibrated" : ""}
        {c.judges ? ` · Judges: ${c.judges}` : ""}
      </p>
    </details>
  );
}

function Card({ c, demo }: { c: GateCard; demo: boolean }) {
  const system = c.bucket === "system";
  return (
    <li className="gate-card" id={`card-${c.id}`} data-gate={c.id}>
      <header className="card-head">
        <span className="mono gate-id">{c.id}</span>
        <h4>{c.name}</h4>
        <span className={`mode-badge mode-${c.mode}`} title={MODES[c.mode].line}>{MODES[c.mode].badge}</span>
        {c.moment ? (
          <span className="moment">
            {c.moment.cliff ? <CliffGlyph /> : null}
            {c.moment.label}
          </span>
        ) : null}
      </header>
      <p className="card-question">{c.question}</p>
      {c.mode === "offline_benchmark" || c.mode === "continuous_aggregate" ? (
        <div className="card-status">
          <p>{c.mode === "offline_benchmark" ? "Offline benchmark — runs on a fixed set of test cases, not on this episode." : "Continuous — measured across real runs over time, not on one episode."}</p>
          {c.statusNote ? <p className="hint">{c.statusNote}</p> : null}
        </div>
      ) : (
        <>
          <ChainRail steps={c.steps} />
          <Result c={c} />
        </>
      )}
      <footer className="card-foot">
        <Details c={c} />
        {system || c.mode === "offline_benchmark" ? null : (
          <Link href={`/evals?view=gates&gate=${c.id}${demo ? "&demo=1" : ""}`} aria-label={`View results for ${c.id}`}>View results</Link>
        )}
      </footer>
    </li>
  );
}

const LINKS: Readonly<Record<string, string>> = {
  context: "What gtm_ai knows now feeds the decision",
  decision: "D10 feeds back into B9: what the person and the customer do becomes the next context",
};

/** The learning loop: Context to Decision, back into Context through D10 and B9, and System beneath ("trust the machinery"). */
export function GateCards({ buckets, demo = false }: { buckets: readonly BucketCards[]; demo?: boolean }) {
  return (
    <div className="gate-cards">
      {buckets.map((b, i) => (
        <div key={b.bucket}>
          {i > 0 ? (
            <div className={`loop-link${b.bucket === "system" ? " loop-system" : ""}`} aria-hidden="true">
              <span>{b.bucket === "system" ? "Trust the machinery underneath both" : LINKS[buckets[i - 1]!.bucket]}</span>
            </div>
          ) : null}
          <section aria-labelledby={`cards-${b.bucket}`} className="cards-bucket" data-bucket={b.bucket}>
            <header>
              <h3 id={`cards-${b.bucket}`}>{b.label}</h3>
              <p className="hint">{b.question}</p>
            </header>
            <ul className="card-grid">
              {b.cards.map((c) => (
                <Card key={c.id} c={c} demo={demo} />
              ))}
            </ul>
          </section>
          {b.bucket === "decision" ? (
            <div className="loop-back" aria-hidden="true">
              <span>↺ back to Context</span>
            </div>
          ) : null}
        </div>
      ))}
    </div>
  );
}
