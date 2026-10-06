"use client";

// The live episode path (HAR-149 section 2): a vertical path in causal order through the gates that apply, each node a human
// question with what happened and what it did. The human's edit sits inline with the gates it caused to run again. A click
// opens the detail drawer; ?gate= opens one on load (the link a failure example uses).
import Link from "next/link";
import { useCallback, useState } from "react";
import { VerdictPill } from "@/components/evals/gates/VerdictPill";
import type { PathGateNode } from "@/lib/evals/inspector/episode-path";
import type { EpisodeInspector } from "@/lib/evals/inspector/load-episode-inspector";
import { withDemo } from "@/lib/view/demo-link";
import { EvalDrawer } from "./EvalDrawer";

function Node({ n, open, onOpen }: { n: PathGateNode; open: boolean; onOpen: (g: string) => void }) {
  const idle = n.status === "waiting" || n.status === "not_applicable" || n.status === "not_recorded";
  return (
    <li className="path-item" data-status={n.status} data-verdict={n.verdict ?? "none"} data-open={open}>
      <button type="button" className="path-node" onClick={() => onOpen(n.gate)} aria-expanded={open} data-gate={n.gate}>
        <span className="path-dot" aria-hidden="true" />
        <span className="path-main">
          <span className="path-question">{n.question}</span>
          <span className="path-status">
            {n.verdict ? <VerdictPill verdict={n.verdict} /> : null}
            <span className="path-status-label">{n.statusLabel}</span>
            {n.effect && !idle ? (
              <span className="path-effect" data-effect={n.effect.headline.toLowerCase().replaceAll(" ", "-")} data-hard-stop={n.effect.hardStop}>
                {n.effect.label}
              </span>
            ) : null}
          </span>
        </span>
        <span className="path-id mono">{n.gate}</span>
      </button>
    </li>
  );
}

export function EpisodePathView({ episode, demo, initialGate }: { episode: EpisodeInspector; demo: boolean; initialGate: string | null }) {
  const [gate, setGate] = useState<string | null>(initialGate && episode.drawers[initialGate] ? initialGate : null);
  const choose = useCallback((g: string | null) => {
    setGate(g);
    const url = new URL(window.location.href);
    if (g) url.searchParams.set("gate", g);
    else url.searchParams.delete("gate");
    window.history.replaceState(null, "", url);
  }, []);
  const close = useCallback(() => choose(null), [choose]);
  const c = episode.path.counts;
  const model = gate ? episode.drawers[gate] : null;

  return (
    <div className="ep-inspector" data-drawer={model ? "open" : "closed"}>
      <header className="ep-head">
        <p className="eyebrow">Live episode</p>
        <h1>{episode.label}</h1>
        {episode.eventSummary ? <blockquote className="ep-event">{episode.eventSummary}</blockquote> : null}
        <p className="ep-status">{episode.statusLine}</p>
        <p className="ep-counts">
          {`${c.ran} of ${c.total} checks have run`}
          {c.failed > 0 ? ` · ${c.failed} failed` : ""}
          {c.warned > 0 ? ` · ${c.warned} with a warning` : ""}
          {c.waiting > 0 ? ` · ${c.waiting} waiting for their trigger` : ""}
          {c.notApplicable > 0 ? ` · ${c.notApplicable} not applicable` : ""}
        </p>
        <p className="ep-links">
          <Link href={withDemo(`/evals/episode/${episode.episodeId}/decision`, demo)}>See the three options</Link>
          {demo ? null : <Link href={`/evals?view=gates&episode=${episode.episodeId}`}>All gate results</Link>}
        </p>
        {episode.notices.map((t) => (
          <p key={t} className="notices" role="status">{t}</p>
        ))}
      </header>
      <ol className="path" aria-label="The checks of this episode in the order they happen">
        {episode.path.items.map((it) =>
          it.type === "gate" ? (
            <Node key={it.gate} n={it} open={gate === it.gate} onOpen={(g) => choose(gate === g ? null : g)} />
          ) : (
            <li key="edit" className="path-item path-edit" data-testid="path-edit">
              <div className="path-edit-card">
                <p className="eyebrow">The human edited the draft</p>
                <p className="path-edit-text">{it.text}</p>
                {it.recomputed.length > 0 ? (
                  <p className="path-edit-reran">
                    <span>The recompute record says it made these stale and re-derived them:</span>
                    {it.recomputed.map((label) => (
                      <span key={label} className="path-edit-chip">{label}</span>
                    ))}
                  </p>
                ) : (
                  <p className="path-edit-reran"><span>No recompute record is stored for this edit.</span></p>
                )}
              </div>
            </li>
          ),
        )}
      </ol>
      {model ? <EvalDrawer model={model} demo={demo} onClose={close} /> : null}
    </div>
  );
}
