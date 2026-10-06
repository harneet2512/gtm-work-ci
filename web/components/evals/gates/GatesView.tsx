import type { GateTableData } from "@/lib/evals/load-gate-table";
import type { GateDefinition } from "@/lib/evals/gate-cards";
import { GateExplorer } from "./GateExplorer";

/**
 * The gate results page body. "Backend unavailable" (a read failed) is said in its own words and is never a FAIL; an empty
 * store is "not measured". Skeleton rows stand in while the page streams (see loading.tsx).
 */
export function GatesView({ data, gates, defs = {}, initialGate = null, modes = {} }: { data: GateTableData; gates: readonly string[]; defs?: Record<string, GateDefinition>; initialGate?: string | null; modes?: Record<string, string> }) {
  if (data.status === "unavailable") {
    return (
      <p className="notices" role="alert" data-state="unavailable">
        The gate results are unavailable right now because the backend could not be reached. This says nothing about any gate: none of them failed.
      </p>
    );
  }
  return (
    <>
      {data.unreadable.length > 0 ? (
        <p className="notices" role="status">{`${data.unreadable.length} ${data.unreadable.length === 1 ? "episode" : "episodes"} could not be read and ${data.unreadable.length === 1 ? "is" : "are"} left out. They are not shown as failures.`}</p>
      ) : null}
      {data.rows.length === 0 ? <p className="hint">No episode has been evaluated yet, so no gate is measured.</p> : null}
      <GateExplorer rows={data.rows} paths={data.paths} gates={gates} defs={defs} initialGate={initialGate} modes={modes} />
    </>
  );
}
