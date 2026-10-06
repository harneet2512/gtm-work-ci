"use client";

// The landing (HAR-149 section 1): three buckets as human questions in a loop. Understand -> decide -> act, and what happens
// feeds back. The four modes are filters over the same gates, never buckets; ids are small and last.
import Link from "next/link";
import { useState } from "react";
import type { ModeKey } from "@/lib/evals/gate-cards";
import { filterGates, type LoopBucket, type LoopModel } from "@/lib/evals/inspector/loop-model";
import { withDemo } from "@/lib/view/demo-link";

function Bucket({ b, active, wide }: { b: LoopBucket; active: boolean; wide?: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <article className="loop-bucket" data-bucket={b.number} data-wide={wide ? "true" : undefined}>
      <p className="loop-num">{`Bucket ${b.number}`}</p>
      <h2>{b.question}</h2>
      <p className="loop-line">{b.line}</p>
      <details open={active || open} onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
        <summary>{`${b.gates.length} ${b.gates.length === 1 ? "check" : "checks"}`}</summary>
        <ul className="loop-gates">
          {b.gates.map((g) => (
            <li key={g.id} data-mode={g.mode}>
              <span className="loop-gate-q">{g.question}</span>
              <span className="mode-badge" data-mode={g.badge.toLowerCase()}>{g.badge}</span>
              <span className="loop-gate-id mono">{g.id}</span>
            </li>
          ))}
        </ul>
      </details>
    </article>
  );
}

export function LoopLanding({ loop, demo, episodeHref }: { loop: LoopModel; demo: boolean; episodeHref: string }) {
  const [mode, setMode] = useState<ModeKey | "all">("all");
  const shown = filterGates(loop.buckets, mode);
  const [one, two, three] = shown;
  return (
    <section className="loop-page">
      <header className="loop-head">
        <p className="eyebrow">How gtm_ai checks its work</p>
        <h1>Three questions, asked again and again</h1>
        <p className="lead">Every email, every recommendation and every edit is checked against the same three questions. The answers loop: what happens next teaches the first question.</p>
      </header>

      <div className="loop-filters" role="group" aria-label="Show checks by when they run">
        <button type="button" aria-pressed={mode === "all"} onClick={() => setMode("all")}>All checks</button>
        {loop.modes.map((m) => (
          <button key={m.id} type="button" aria-pressed={mode === m.id} onClick={() => setMode(mode === m.id ? "all" : m.id)} title={m.line}>
            {`${m.badge} · ${m.count}`}
          </button>
        ))}
      </div>

      <div className="loop-flow" aria-label="The loop">
        <Bucket b={one!} active={mode !== "all"} />
        <span className="loop-arrow" aria-hidden="true">→</span>
        <Bucket b={two!} active={mode !== "all"} />
        <span className="loop-arrow" aria-hidden="true">→</span>
        <article className="loop-exec" data-testid="loop-execution">
          <p className="loop-num">Then</p>
          <h2>The action happens</h2>
          <p className="loop-line">The approved message goes out, once, to the right person.</p>
        </article>
      </div>
      <p className="loop-feedback" data-testid="loop-feedback">
        <span aria-hidden="true">↩</span> {loop.feedback}
      </p>

      <div className="loop-trust">
        <Bucket b={three!} active={mode !== "all"} wide />
      </div>

      <p className="loop-cta">
        <Link href={withDemo(episodeHref, demo)}>Follow one real episode through these checks</Link>
      </p>
    </section>
  );
}
