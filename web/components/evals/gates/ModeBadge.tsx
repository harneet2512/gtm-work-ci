"use client";

import { useId, useState } from "react";
import { explainGate, MODES, type GateCard } from "@/lib/evals/gate-cards";

/**
 * The execution-mode badge. In Demo mode it is a button: hover or click shows three lines from the registry (what the gate
 * is, when it runs as mode · trigger, and what a failure changes). Outside Demo mode it is the plain badge with its title.
 */
export function ModeBadge({ card, demo }: { card: GateCard; demo: boolean }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const badge = MODES[card.mode].badge;
  if (!demo) return <span className={`mode-badge mode-${card.mode}`} title={MODES[card.mode].line}>{badge}</span>;
  const e = explainGate(card);
  return (
    <span className="mode-explain" onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)}>
      <button type="button" className={`mode-badge mode-${card.mode}`} aria-expanded={open} aria-controls={id} onClick={() => setOpen(true)} onKeyDown={(e) => e.key === "Escape" && setOpen(false)} onFocus={() => setOpen(true)} onBlur={() => setOpen(false)}>
        {badge}
      </button>
      {open ? (
        <div className="explain-pop" id={id} role="tooltip" data-gate={card.id}>
          <dl>
            <div><dt>What it is</dt><dd>{e.what}</dd></div>
            <div><dt>When it runs</dt><dd>{e.when}</dd></div>
            <div><dt>What failure changes</dt><dd>{e.changes}</dd></div>
          </dl>
        </div>
      ) : null}
    </span>
  );
}
