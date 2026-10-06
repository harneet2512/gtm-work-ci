// The nine sections of the eval detail drawer (HAR-149), each a small pure view of the DrawerModel. Questions first, ids last;
// anything not measured says so in words. Raw JSON is under "Advanced" and never in Demo mode.
import Link from "next/link";
import type { ReactNode } from "react";
import { VerdictPill } from "@/components/evals/gates/VerdictPill";
import type { CriterionResult, FoldedVerdict } from "@/lib/evals/inspector/criteria";
import type { DrawerModel } from "@/lib/evals/inspector/drawer-model";
import { inputKindLabel } from "@/lib/evals/inspector/inputs";
import { messageOf } from "@/lib/evals/naming";
import { withDemo } from "@/lib/view/demo-link";

type Verdict = FoldedVerdict;

/** A criterion result as the shared verdict pill; "not applicable" gets its own quiet pill (never a pass). */
export function ResultPill({ result }: { result: CriterionResult | Verdict | null }) {
  if (result === "not_applicable") return <VerdictPill verdict={null} notTriggered />;
  return <VerdictPill verdict={result} />;
}

export function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section className="dr-section" aria-labelledby={`dr-${id}`} data-section={id}>
      <h3 id={`dr-${id}`}>{title}</h3>
      {children}
    </section>
  );
}

export function WhatSection({ m }: { m: DrawerModel }) {
  return <p className="dr-plain">{m.what}</p>;
}

export function WhySection({ m }: { m: DrawerModel }) {
  return (
    <>
      <p className="dr-plain">{m.why.text}</p>
      {m.why.invariant ? (
        <p className="dr-aside">
          <span className="dr-key">It catches</span> {m.why.invariant}
        </p>
      ) : null}
    </>
  );
}

export function WhenSection({ m }: { m: DrawerModel }) {
  const msg = m.when.message ? messageOf(m.when.message) : null;
  return (
    <dl className="dr-facts">
      <dt>Part of</dt>
      <dd>{m.when.bucket}</dd>
      <dt>Mode</dt>
      <dd>
        <span className="mode-badge" data-mode={m.when.modeBadge.toLowerCase()}>{m.when.modeBadge}</span> {m.when.modeLine}
      </dd>
      {m.when.trigger ? (
        <>
          <dt>Runs</dt>
          <dd>{m.when.trigger}</dd>
        </>
      ) : null}
      {m.when.moment ? (
        <>
          <dt>Demo moment</dt>
          <dd>{m.when.moment}</dd>
        </>
      ) : null}
      {m.when.message ? (
        <>
          <dt>Shown in</dt>
          <dd>{msg?.chip ?? m.when.message}</dd>
        </>
      ) : null}
    </dl>
  );
}

export function InputsSection({ m, demo }: { m: DrawerModel; demo: boolean }) {
  return (
    <>
      {m.inputs.items.length === 0 ? <p className="hint">{m.measured ? "This result cites no inputs, so it cannot read as a pass." : "Nothing went into it yet: it has not run in this episode."}</p> : null}
      <ul className="dr-inputs">
        {m.inputs.items.map((i, n) => (
          <li key={`${i.kind}-${n}`} data-kind={i.kind}>
            <span className="dr-kind">{i.kindLabel ?? inputKindLabel(i.kind)}</span>
            <strong>{i.href ? <Link href={withDemo(i.href, demo)}>{i.title}</Link> : i.title}</strong>
            {i.text ? <blockquote>{i.text}</blockquote> : <p className="hint">The text of this record is not loaded here.</p>}
          </li>
        ))}
      </ul>
      {demo || !m.measured ? null : (
        <details className="dr-advanced">
          <summary>Advanced</summary>
          <pre>{m.inputs.advancedJson}</pre>
        </details>
      )}
    </>
  );
}

function GraderLine({ m }: { m: DrawerModel }) {
  const g = m.how.grader;
  const who = g.kind === "deterministic" ? "Checked by code from the stored records" : g.kind === "hybrid" ? "Code checks plus a model reading" : g.kind === "model" ? "Read by a model" : "Grader not stated";
  return (
    <p className="dr-grader">
      <strong>{who}.</strong>
      {g.model ? ` Model ${g.model}.` : ""}
      {g.evaluator ? <span className="mono"> {g.evaluator}</span> : null}
      {g.kind !== "deterministic" ? <span className="dr-calib"> Not yet calibrated: the reference answers are pending.</span> : null}
    </p>
  );
}

export function HowSection({ m }: { m: DrawerModel }) {
  const c = m.how.criteria;
  const facts = [
    m.how.modelCalls === null ? "Model calls: not recorded" : m.how.modelCalls === 0 ? "No model call: the answer was replayed from a recording" : `${m.how.modelCalls} live model ${m.how.modelCalls === 1 ? "call" : "calls"}`,
    m.how.latencyMs !== null ? `Model time ${m.how.latencyMs} ms` : null,
    m.how.tokens !== null ? `${m.how.tokens} tokens` : null,
    m.how.costUsd !== null ? `$${m.how.costUsd}` : null,
  ].filter(Boolean);
  return (
    <>
      <GraderLine m={m} />
      {c.groups.length === 0 ? <p className="hint">No check has run, so there is nothing to show here.</p> : null}
      {c.groups.map((g) => (
        <div key={g.key} className="dr-group">
          {g.title ? <h4>{g.title}</h4> : null}
          <ul className="dr-criteria">
            {g.rows.map((r, n) => (
              <li key={`${r.id}-${n}`} data-result={r.result}>
                <span className="dr-crit-label">{r.label}</span>
                <ResultPill result={r.result} />
                {r.result !== "pass" && r.why ? <span className="dr-crit-why">{r.why}</span> : null}
              </li>
            ))}
          </ul>
        </div>
      ))}
      {c.result ? (
        <p className="dr-fold">
          <span className="dr-crit-label">Result</span>
          <ResultPill result={c.result} />
        </p>
      ) : null}
      {c.groups.length > 0 ? <p className="dr-aside">{c.folding}</p> : null}
      {c.note ? <p className="dr-aside">{c.note}</p> : null}
      {c.groups.length > 0 ? <p className="dr-aside">No numeric score: gtm_ai reports a verdict per criterion, not a number.</p> : null}
      {facts.length > 0 ? <p className="dr-aside">{facts.join(" · ")}</p> : null}
    </>
  );
}

export function HappenedSection({ m }: { m: DrawerModel }) {
  const h = m.happened;
  return (
    <>
      <p className="dr-fold">
        <ResultPill result={h.verdict} />
        <span>{h.statement}</span>
      </p>
      {h.why ? <p className="dr-plain">{h.why}</p> : null}
      {h.failedChecks.length > 0 ? (
        <>
          <h4>Checks that did not pass</h4>
          <ul className="dr-criteria">
            {h.failedChecks.map((r, n) => (
              <li key={`${r.id}-${n}`}>
                <span className="dr-crit-label">{r.label}</span>
                <ResultPill result={r.result} />
                <span className="dr-crit-why">{r.why}</span>
              </li>
            ))}
          </ul>
        </>
      ) : null}
      {h.evidence.length > 0 ? (
        <>
          <h4>Evidence</h4>
          <ul className="dr-evidence">
            {h.evidence.map((e) => (
              <li key={e.ref}>
                <span>{e.summary ?? e.label}</span>
                {e.source ? <span className="dr-source">{e.source}</span> : null}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      {h.confidence !== null ? <p className="dr-aside">{`The grader's own stated confidence: ${Math.round(h.confidence * 100)}%.`}</p> : null}
    </>
  );
}

export function EffectSection({ m }: { m: DrawerModel }) {
  const e = m.effect;
  return (
    <>
      <p className="dr-effect">
        <span className="dr-effect-word" data-effect={e.headline.toLowerCase().replaceAll(" ", "-")} data-hard-stop={e.hardStop}>{e.headline}</span>
        <span>{e.plain}</span>
      </p>
      {e.lineage ? (
        <p className="dr-aside">{`The recompute record says this result replaced an earlier run${e.lineage.previousVerdict ? ` that said ${e.lineage.previousVerdict.toUpperCase()}` : ""}.`}</p>
      ) : null}
      {!m.measured ? <p className="hint">It has not run in this episode, so it has had no effect.</p> : null}
      <dl className="dr-facts">
        {e.impact ? (
          <>
            <dt>What a failure changes</dt>
            <dd>{e.impact}</dd>
          </>
        ) : null}
        {e.basis ? (
          <>
            <dt>Where that is decided</dt>
            <dd className="mono">{e.basis}</dd>
          </>
        ) : null}
      </dl>
    </>
  );
}

export function PerformanceSection({ m }: { m: DrawerModel }) {
  return (
    <ul className="dr-metrics">
      {m.performance.metrics.map((x) => (
        <li key={x.id} data-state={x.state}>
          <span className="dr-crit-label">{x.label}</span>
          {x.state === "measured" ? <strong>{x.value}</strong> : <span className="dr-nm">Not measured yet</span>}
          <span className="dr-crit-why">{x.state === "measured" ? x.note : x.note}</span>
          {x.state === "not_measured" ? <span className="dr-will">{`It will measure: ${x.measures.charAt(0).toLowerCase()}${x.measures.slice(1)}`}</span> : null}
        </li>
      ))}
    </ul>
  );
}

export function FailuresSection({ m, demo }: { m: DrawerModel; demo: boolean }) {
  if (m.failures.empty) return <p className="hint">{m.failures.empty}</p>;
  return (
    <ul className="dr-failures">
      {m.failures.categories.map((c) => (
        <li key={c.id}>
          <p>
            <strong>{c.label}</strong> <span className="dr-count">{`${c.count} ${c.count === 1 ? "time" : "times"}`}</span>
          </p>
          <ul>
            {c.examples.map((x) => (
              <li key={x.episodeId}>
                <Link href={withDemo(`/evals/episode/${x.episodeId}?gate=${m.gate}`, demo)}>{x.episodeLabel}</Link>
                {x.why ? <span className="dr-crit-why">{x.why}</span> : null}
              </li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  );
}
