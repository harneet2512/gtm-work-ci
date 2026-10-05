"use client";

import type { ExplorerModel } from "@/lib/graph/model";

const CRUMB_MAX = 22;

const clip = (text: string): string => (text.length > CRUMB_MAX ? `${text.slice(0, CRUMB_MAX - 1)}…` : text);

/** The trail of visited nodes, newest last; each one leads back. */
export function GraphCrumbs({ model, trail, selectedId, onJump }: { model: ExplorerModel; trail: readonly string[]; selectedId: string | null; onJump: (id: string) => void }) {
  const nodes = trail.map((id) => model.byId.get(id)).filter((n) => n !== undefined);
  if (nodes.length === 0) return null;
  return (
    <nav className="gx-crumbs" aria-label="Visited nodes">
      <ol>
        {nodes.map((node) => (
          <li key={node.id}>
            <button type="button" aria-current={node.id === selectedId ? "true" : undefined} aria-label={`Back to ${node.name}`} title={node.name} onClick={() => onJump(node.id)}>
              {clip(node.label)}
            </button>
          </li>
        ))}
      </ol>
    </nav>
  );
}

interface ZoomProps {
  onZoom: (direction: 1 | -1) => void;
  onFit: () => void;
  expanded: boolean;
  onExpand: () => void;
}

/** Zoom in, zoom out, fit-all, and expand the graph to fill the workspace. */
export function GraphZoom({ onZoom, onFit, expanded, onExpand }: ZoomProps) {
  return (
    <div className="gx-zoom" role="group" aria-label="Zoom">
      <button type="button" aria-label="Zoom in" title="Zoom in (+)" onClick={() => onZoom(1)}>
        +
      </button>
      <button type="button" aria-label="Zoom out" title="Zoom out (−)" onClick={() => onZoom(-1)}>
        −
      </button>
      <button type="button" aria-label="Fit the whole graph" title="Fit the whole graph (0 or Esc)" onClick={onFit}>
        <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false">
          <circle cx="7" cy="7" r="3.2" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M7 0.5v2.5M7 11v2.5M0.5 7H3M11 7h2.5" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      </button>
      <button type="button" aria-label={expanded ? "Leave the expanded view" : "Expand the graph"} aria-pressed={expanded} title={expanded ? "Leave the expanded view (Esc or F)" : "Expand the graph (F)"} onClick={onExpand}>
        <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false">
          <path d={expanded ? "M5 1v4H1M9 1v4h4M13 9H9v4M1 9h4v4" : "M1 5V1h4M9 1h4v4M13 9v4H9M5 13H1V9"} fill="none" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      </button>
    </div>
  );
}
