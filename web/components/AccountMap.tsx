"use client";

import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { ZOOM_STEP } from "@/lib/graph/camera";
import type { ClaimIndex } from "@/lib/graph/claim-index";
import type { FieldHistory } from "@/lib/graph/history";
import type { ExplorerModel } from "@/lib/graph/model";
import type { FocusMove } from "@/lib/graph/render/engine";
import { ghostSelection, selectionForNode } from "@/lib/graph/selection";
import { chronologicalActivities, pushTrail, stepThrough } from "@/lib/graph/stepping";
import { diffRows } from "@/lib/graph/transition";
import { useGraphEngine } from "@/lib/graph/use-graph-engine";
import type { PlayView } from "@/lib/view/account-view";
import type { DiffIndex } from "@/lib/view/diff";
import { edgeSelection, type Selection } from "@/lib/view/provenance";
import { GraphCrumbs, GraphZoom } from "./graph/GraphControls";
import { GraphDiffPanel } from "./graph/GraphDiffPanel";
import { GraphLegend } from "./graph/GraphLegend";
import { GraphSearch } from "./graph/GraphSearch";
import { GraphTextAlternative } from "./graph/GraphTextAlternative";

interface Props {
  model: ExplorerModel;
  marks: DiffIndex;
  /** "after" shows what the event changed beside the canvas; null when no event is inspected. */
  playView: PlayView | null;
  claims: ClaimIndex;
  /** What each state field was just before the event (After Play), for "used to be". */
  history: FieldHistory;
  selectedId: string | null;
  onSelect: (selection: Selection) => void;
  onClear: () => void;
  /** The inspector for the selection, shown in a sheet beside the canvas. */
  details: ReactNode;
  /** What the graph holds against the record, in a sentence. */
  coverage: string;
}

const HELP = "Scroll or pinch to zoom, drag the background to move, drag a node to rearrange. Click a node for details, double-click to dive in, Escape to see everything. Left and right arrows step through activities in time order. F expands the graph.";
/** The live account graph: a canvas explorer with search, a trail, the event's diff and a text alternative. */
export function AccountMap(props: Props) {
  if (props.model.nodes.length === 0) return <p className="empty">No graph around this account yet.</p>;
  return <GraphExplorer {...props} />;
}

function GraphExplorer({ model, marks, playView, claims, history, selectedId, onSelect, onClear, details, coverage }: Props) {
  const [trail, setTrail] = useState<readonly string[]>([]);
  const [expanded, setExpanded] = useState(false);
  const provId = useId();
  const order = useMemo(() => chronologicalActivities(model.nodes), [model]);
  const rows = useMemo(() => diffRows(model, marks), [model, marks]);
  const helpId = useId();
  // How the camera should follow the next selection (set by whatever made it).
  const move = useRef<FocusMove>("fly");

  const selectNode = useCallback(
    (id: string, how: FocusMove): void => {
      // A ghost (what the event took out of the view) opens with what it was and what changed.
      const selection = selectionForNode(model, id, claims, history) ?? ghostSelection(model, id);
      if (!selection) return;
      move.current = how;
      onSelect(selection);
    },
    [model, claims, history, onSelect],
  );

  const selectEdge = (id: string): void => {
    const edge = model.edgeById.get(id);
    if (!edge) return;
    const label = (n: string): string => model.byId.get(n)?.label ?? n;
    const selection = edgeSelection(edge.raw, label(edge.source), label(edge.target));
    onSelect({ ...selection, title: `${edge.label}: ${label(edge.source)} → ${label(edge.target)}` });
  };

  const { canvasRef, hostRef, engineRef } = useGraphEngine(model, {
    onPick: (id) => selectNode(id, "fly"),
    onDive: (id) => {
      engineRef.current?.focus(id, "dive");
      selectNode(id, "none");
    },
    onBackground: onClear,
  });

  // The selection drives the camera: whatever selected a node (canvas, search, arrows, timeline, inspector),
  // the camera follows it and the trail records it.
  useEffect(() => {
    const id = selectedId !== null && (model.byId.has(selectedId) || model.ghostById.has(selectedId)) ? selectedId : null;
    engineRef.current?.focus(id, id === null ? "none" : move.current);
    move.current = "fly";
    if (id !== null) setTrail((t) => pushTrail(t, id));
  }, [selectedId, model, engineRef]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    const target = e.target as HTMLElement;
    if (target.closest(".gx-search")) return;
    const engine = engineRef.current;
    if (e.key === "Escape" && expanded) {
      // Escape first leaves the expanded view; the next one backs out to everything.
      e.preventDefault();
      setExpanded(false);
    } else if (e.key === "f" || e.key === "F") {
      e.preventDefault();
      setExpanded((x) => !x);
    } else if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
      const next = stepThrough(order, selectedId, e.key === "ArrowRight" ? 1 : -1);
      if (next === null) return;
      e.preventDefault();
      selectNode(next, "fly");
    } else if (e.key === "Escape") {
      e.preventDefault();
      onClear();
      engine?.fit();
    } else if (e.key === "+" || e.key === "=") {
      e.preventDefault();
      engine?.zoomBy(ZOOM_STEP);
    } else if (e.key === "-" || e.key === "_") {
      e.preventDefault();
      engine?.zoomBy(1 / ZOOM_STEP);
    } else if (e.key === "0") {
      e.preventDefault();
      engine?.fit();
    } else if (e.key === "Enter" && target === canvasRef.current && selectedId !== null && model.byId.has(selectedId)) {
      e.preventDefault();
      engine?.focus(selectedId, "dive");
    }
  };

  const jump = (id: string): void => {
    selectNode(id, "fly");
    canvasRef.current?.focus();
  };

  const zoom = (direction: 1 | -1): void => engineRef.current?.zoomBy(direction === 1 ? ZOOM_STEP : 1 / ZOOM_STEP);
  const rail = playView === "after";

  return (
    <div className={expanded ? "gx is-expanded" : "gx"} data-testid="account-map" role="group" aria-label="Account map" onKeyDown={onKeyDown}>
      <div className="gx-body">
        <div className="gx-stage" ref={hostRef} data-entrance="none" data-settled="false">
          <canvas ref={canvasRef} className="gx-canvas" tabIndex={0} role="application" aria-roledescription="graph" aria-label="Account graph" aria-describedby={helpId} />
          <div className="gx-top">
            <GraphSearch nodes={model.nodes} onJump={jump} />
            <GraphZoom onZoom={zoom} onFit={() => engineRef.current?.fit()} expanded={expanded} onExpand={() => setExpanded((x) => !x)} />
          </div>
          <GraphCrumbs model={model} trail={trail} selectedId={selectedId} onJump={jump} />
          <div className="gx-foot">
            <GraphLegend nodes={model.nodes} showStatus={rail} />
            <p className="gx-hud">
              {`${model.nodes.length} nodes · ${model.edges.length} edges · `}
              <span data-hud-zoom="" />
            </p>
          </div>
        </div>
        <aside className="gx-sheet" aria-label="Details">
          <section aria-labelledby={provId} className="gx-details">
            <h2 id={provId}>Provenance</h2>
            {details}
          </section>
          {rail ? <GraphDiffPanel rows={rows} onJump={jump} /> : null}
        </aside>
      </div>
      <p className="gx-coverage">{coverage}</p>
      <p id={helpId} className="gx-help">
        {HELP}
      </p>
      <GraphTextAlternative model={model} selectedId={selectedId} onNode={(id) => selectNode(id, "fly")} onEdge={selectEdge} />
    </div>
  );
}
