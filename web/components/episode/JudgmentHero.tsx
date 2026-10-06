"use client";

import Link from "next/link";
import { useState } from "react";
import type { DeltaView, Hero } from "@/lib/view/judgment-hero";

/** The Judgment Delta: class chip, the changed words highlighted (insertions and deletions), and a toggle for the full edit. */
function Delta({ delta }: { delta: DeltaView }) {
  const [full, setFull] = useState(false);
  return (
    <div className="delta-view" data-testid="judgment-delta">
      <span className="msg-chip delta-chip" data-classified={delta.chip !== "Not classified yet"}>{delta.chip}</span>
      {delta.statement ? <p className="delta-statement">{delta.statement}</p> : null}
      {delta.diff && delta.diff.segments.length > 0 ? (
        <p className="delta-diff" aria-label="Changed words">
          {delta.diff.segments.map((s, i) =>
            s.kind === "del" ? <del key={i}>{s.text}</del> : s.kind === "ins" ? <ins key={i}>{s.text}</ins> : <span key={i}>{s.text}</span>,
          )}
        </p>
      ) : null}
      {delta.plain ? <p className="delta-diff">{delta.plain}</p> : null}
      {delta.strength ? <p className="hint">{`Signal strength: ${delta.strength}`}</p> : null}
      {delta.diff && delta.diff.truncated ? (
        <>
          <button type="button" className="delta-toggle" aria-expanded={full} onClick={() => setFull(!full)}>{full ? "Hide full edit" : "Show full edit"}</button>
          {full ? (
            <div className="delta-full">
              <p><strong>Before</strong> {delta.diff.full.before}</p>
              <p><strong>After</strong> {delta.diff.full.after}</p>
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

/**
 * The Judgment Episode hero: Message 1, 2 and 3 side by side, each with its Cliff (Slack) rendering and its evals one click
 * away. The presenter hint is shown in Demo mode only and can be dismissed.
 */
export function JudgmentHero({ hero, episodeId, demo }: { hero: Hero; episodeId: string; demo: boolean }) {
  const [hintOpen, setHintOpen] = useState(true);
  const suffix = demo ? "&demo=1" : "";
  return (
    <section className="judgment-hero" aria-labelledby="hero-h" data-view="judgment-hero">
      <header>
        <h2 id="hero-h">{hero.title}</h2>
        <span className="hint">One loop, explained by Cliff&apos;s three messages.</span>
      </header>
      {demo && hintOpen ? (
        <p className="hero-hint" role="note" data-testid="presenter-hint">
          <span>{hero.demoHint}</span>
          <button type="button" onClick={() => setHintOpen(false)} aria-label="Dismiss the presenter hint">Dismiss</button>
        </p>
      ) : null}
      <div className="hero-messages">
        {hero.messages.map((m) => (
          <article key={m.id} className="hero-message" data-message={m.id}>
            <h3>{m.title}</h3>
            <dl>
              {m.fields.map((f) => (
                <div key={f.label}>
                  <dt>{f.label}</dt>
                  {f.delta ? (
                    <dd data-state="value"><Delta delta={f.delta} /></dd>
                  ) : (
                    f.lines.map((l, i) => (
                      <dd key={i} data-state={f.state}>{l}</dd>
                    ))
                  )}
                </div>
              ))}
            </dl>
            <p className="hero-links">
              <Link href={`/episodes/${episodeId}?mode=cliff${suffix}`}>{`Slack rendering of ${m.id}`}</Link>
              <Link href={`/evals?view=cards${suffix}#cards-${m.id}`}>{`Evals of ${m.id}`}</Link>
            </p>
          </article>
        ))}
      </div>
    </section>
  );
}
