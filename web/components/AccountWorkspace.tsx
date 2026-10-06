"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AccountState, Activity } from "@/lib/api/types";
import { indexClaims } from "@/lib/graph/claim-index";
import { coverageOf } from "@/lib/graph/coverage";
import { fieldHistory } from "@/lib/graph/history";
import { layoutMemory } from "@/lib/graph/memory";
import { buildExplorerModel } from "@/lib/graph/model";
import { ghostSelection, selectionForNode } from "@/lib/graph/selection";
import type { AccountView } from "@/lib/view/account-view";
import { buildSnapshot } from "@/lib/view/account-snapshot";
import { resolveProvenance, type Selection } from "@/lib/view/provenance";
import { AccountMap } from "./AccountMap";
import { AccountSnapshotView } from "./AccountSnapshot";
import { ClaimsList } from "./ClaimsList";
import { DiffSummary } from "./DiffSummary";
import { ProvenancePanel } from "./ProvenancePanel";
import { Timeline } from "./Timeline";

interface Props {
  view: AccountView;
  state: AccountState | null;
  /** After Play: the state just before the event, so a changed fact can say what it used to be. */
  stateBefore?: AccountState | null;
  /** Every loaded activity, including ones outside the visible window: provenance resolves against these. */
  activities: readonly Activity[];
  /** The event being inspected, if any (the diff panel only makes sense then). */
  eventId?: string | null;
  /** Whether an event is being inspected (the diff panel only makes sense then). */
  hasEvent: boolean;
}

/** Holds the one piece of interactive state, the current selection, shared by map, claims and timeline. */
export function AccountWorkspace({ view, state, stateBefore = null, activities, eventId = null, hasEvent }: Props) {
  const [selection, setSelection] = useState<Selection | null>(null);
  const lookup = useMemo(() => new Map(activities.map((a) => [a.id, a])), [activities]);
  const claims = useMemo(() => indexClaims(state), [state]);
  const history = useMemo(() => fieldHistory(stateBefore, state), [stateBefore, state]);
  // What the previous view called each node (browser memory, read after mount so the server render matches):
  // names the ghosts of what this event took out of the view.
  const [previousLabels, setPreviousLabels] = useState<ReadonlyMap<string, string>>(() => new Map());
  useEffect(() => {
    if (view.showMarks) setPreviousLabels(layoutMemory().recall(view.graph.account_id)?.labels ?? new Map());
  }, [view.graph.account_id, view.showMarks]);
  const model = useMemo(
    () => buildExplorerModel({ graph: view.graph, marks: view.marks, showMarks: view.showMarks, activities, state, eventId, previousLabels }),
    [view.graph, view.marks, view.showMarks, activities, state, eventId, previousLabels],
  );
  const coverage = useMemo(() => coverageOf(model, activities, claims).text, [model, activities, claims]);
  const mapRef = useRef<HTMLElement | null>(null);
  // A claim or a timeline item picked below the graph brings the graph (and its inspector) back into view.
  const selectFromList = useCallback((s: Selection) => {
    setSelection(s);
    mapRef.current?.scrollIntoView?.({ block: "nearest" });
  }, []);
  const provenance = useMemo(() => (selection ? resolveProvenance(selection, lookup) : null), [selection, lookup]);
  const snapshot = useMemo(() => buildSnapshot(state, activities), [state, activities]);
  const selectedId = selection?.id ?? null;
  const clear = useCallback(() => setSelection(null), []);
  const open = useCallback((id: string) => setSelection(selectionForNode(model, id, claims, history) ?? ghostSelection(model, id)), [model, claims, history]);

  return (
    <div className="workspace" data-view={view.view}>
      <section aria-labelledby="map-h" className="panel map-panel" ref={mapRef}>
        <h2 id="map-h">{view.view === "before" ? "Before Play: account map" : "After Play: what changed"}</h2>
        <AccountMap
          model={model} marks={view.marks} playView={hasEvent ? view.view : null} claims={claims}
          history={history}
          selectedId={selectedId}
          onSelect={setSelection}
          onClear={clear}
          details={<ProvenancePanel provenance={provenance} onOpen={open} />}
          coverage={coverage}
        />
        {view.graph.truncated ? <p className="hint">The core truncated this neighborhood; some nodes are not shown.</p> : null}
        {view.graph.projection.complete ? null : <p className="hint warn">The graph projection is still catching up with the database.</p>}
        {model.droppedEdges > 0 ? <p className="hint">{`${model.droppedEdges} edges point outside this neighborhood and are not drawn.`}</p> : null}
      </section>
      <AccountSnapshotView snapshot={snapshot} />
      <section aria-labelledby="claims-h" className="panel claims-panel">
        <h2 id="claims-h">Claims</h2>
        {state ? <ClaimsList fields={state.fields} selectedId={selectedId} onSelect={selectFromList} /> : <p className="empty">No account state computed yet.</p>}
      </section>
      <section aria-labelledby="time-h" className="panel time-panel">
        <h2 id="time-h">Timeline</h2>
        <Timeline activities={view.timeline} cutoff={view.cutoff} eventActivityIds={view.eventActivityIds} selectedId={selectedId} onSelect={selectFromList} />
      </section>
      {hasEvent ? (
        <section aria-labelledby="diff-h" className="panel diff-panel">
          <h2 id="diff-h">Graph diff of the Play event</h2>
          {view.view === "after" ? <DiffSummary marks={view.marks} labels={new Map([...model.nodes, ...model.ghosts].map((n) => [n.id, n.label]))} /> : <p className="empty">Switch to After Play to see what the event changed.</p>}
        </section>
      ) : null}
    </div>
  );
}
