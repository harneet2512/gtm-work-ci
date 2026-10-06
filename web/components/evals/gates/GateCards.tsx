import Link from "next/link";
import { CHAIN, CRITERION_ORDER, runsHint, type GateCard, type MessageCards } from "@/lib/evals/gate-cards";
import { messageOf } from "@/lib/evals/naming";
import { ModeBadge } from "./ModeBadge";
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
    if (c.mode === "live_conditional" && c.notTriggered) {
      return (
        <p className="card-result hint" data-state="not-triggered">
          <VerdictPill verdict={null} notTriggered /> {`Not applicable — ${c.notTriggered} in this episode`}
        </p>
      );
    }
    return (
      <p className="card-result hint" data-state="not-run">
        <VerdictPill verdict={null} /> {`Not run yet — ${runsHint(c.moment).replace(/^Runs/, "runs")}`}
      </p>
    );
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
  if (!c.invariant && !c.trigger && given.length === 0 && !c.criteriaNote && !c.impactBasis) return null;
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
      {c.impactBasis ? <p><strong>Why failure changes this:</strong> {c.impactBasis}</p> : null}
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
        {c.message ? <span className="msg-chip" data-message={c.message}>{messageOf(c.message)?.chip}</span> : null}
        <ModeBadge card={c} demo={demo} />
        {c.moment ? (
          <span className="moment">
            {c.moment.cliff ? <CliffGlyph /> : null}
            {c.moment.label}
          </span>
        ) : null}
      </header>
      <p className="card-question">{c.question}</p>
      <p className="impact-line" data-impact={c.impact ?? ""}>
        <strong>What failure changes:</strong> {c.impact ?? "not stated"} <span className="bucket-tag">· {c.bucketLabel}</span>
      </p>
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
  M2: "What changed in Message 1 feeds the decision in Message 2",
  M3: "The human's choice and edit become what we learn in Message 3",
  ecolite: "What we learned is used next time: EcoLite Play applies it to the next case",
  system: "Trust the machinery underneath all three messages",
};

/** The one loop: Message 1, 2, 3, then EcoLite Play back into Message 1 on the next case, with System Trust beneath. */
export function GateCards({ buckets: groups, demo = false }: { buckets: readonly MessageCards[]; demo?: boolean }) {
  return (
    <div className="gate-cards">
      {groups.map((g, i) => (
        <div key={g.message}>
          {i > 0 ? (
            <div className={`loop-link${g.message === "system" ? " loop-system" : ""}`} aria-hidden="true">
              <span>{LINKS[g.message]}</span>
            </div>
          ) : null}
          <section aria-labelledby={`cards-${g.message}`} className="cards-bucket message-section" data-message={g.message}>
            <header>
              <h3 id={`cards-${g.message}`}>{g.title}</h3>
              <p className="hint">{g.line}</p>
            </header>
            <ul className="card-grid">
              {g.cards.map((c) => (
                <Card key={c.id} c={c} demo={demo} />
              ))}
            </ul>
          </section>
          {g.message === "ecolite" ? (
            <div className="loop-back" aria-hidden="true">
              <span>↺ back to Message 1 on the next case</span>
            </div>
          ) : null}
        </div>
      ))}
    </div>
  );
}
