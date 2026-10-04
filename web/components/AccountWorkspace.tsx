"use client";

import { useMemo, useState } from "react";
import type { AccountState, Activity } from "@/lib/api/types";
import type { AccountView } from "@/lib/view/account-view";
import { resolveProvenance, type Selection } from "@/lib/view/provenance";
import { AccountMap } from "./AccountMap";
import { ClaimsList } from "./ClaimsList";
import { DiffSummary } from "./DiffSummary";
import { ProvenancePanel } from "./ProvenancePanel";
import { Timeline } from "./Timeline";

interface Props {
  view: AccountView;
  state: AccountState | null;
  /** Every loaded activity, including ones outside the visible window: provenance resolves against these. */
  activities: readonly Activity[];
  /** Whether an event is being inspected (the diff panel only makes sense then). */
  hasEvent: boolean;
}

/** Holds the one piece of interactive state, the current selection, shared by map, claims and timeline. */
export function AccountWorkspace({ view, state, activities, hasEvent }: Props) {
  const [selection, setSelection] = useState<Selection | null>(null);
  const lookup = useMemo(() => new Map(activities.map((a) => [a.id, a])), [activities]);
  const labels = useMemo(() => new Map(view.graph.nodes.map((n) => [n.id, n.label])), [view.graph]);
  const provenance = useMemo(() => (selection ? resolveProvenance(selection, lookup) : null), [selection, lookup]);
  const selectedId = selection?.id ?? null;

  return (
    <div className="workspace" data-view={view.view}>
      <section aria-labelledby="map-h" className="panel map-panel">
        <h2 id="map-h">{view.view === "before" ? "Before Play: account map" : "After Play: what changed"}</h2>
        <AccountMap graph={view.graph} marks={view.marks} showMarks={view.showMarks} selectedId={selectedId} onSelect={setSelection} />
        {view.graph.truncated ? <p className="hint">The core truncated this neighborhood; some nodes are not shown.</p> : null}
        {view.graph.projection.complete ? null : <p className="hint warn">The graph projection is still catching up with the database.</p>}
      </section>
      <section aria-labelledby="prov-h" className="panel prov-panel">
        <h2 id="prov-h">Provenance</h2>
        <ProvenancePanel provenance={provenance} />
      </section>
      <section aria-labelledby="claims-h" className="panel claims-panel">
        <h2 id="claims-h">Claims</h2>
        {state ? <ClaimsList fields={state.fields} selectedId={selectedId} onSelect={setSelection} /> : <p className="empty">No account state computed yet.</p>}
      </section>
      <section aria-labelledby="time-h" className="panel time-panel">
        <h2 id="time-h">Timeline</h2>
        <Timeline activities={view.timeline} cutoff={view.cutoff} eventActivityIds={view.eventActivityIds} selectedId={selectedId} onSelect={setSelection} />
      </section>
      {hasEvent ? (
        <section aria-labelledby="diff-h" className="panel diff-panel">
          <h2 id="diff-h">Graph diff of the Play event</h2>
          {view.view === "after" ? <DiffSummary marks={view.marks} labels={labels} /> : <p className="empty">Switch to After Play to see what the event changed.</p>}
        </section>
      ) : null}
    </div>
  );
}
