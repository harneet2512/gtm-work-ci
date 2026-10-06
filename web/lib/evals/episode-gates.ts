// The episode's stored gate results as rows (R1 applied: a pass without evidence is unknown), plus a row for each per-episode
// gate with no result, for the episode page's protection view and the hero. Reads only what the loader already holds.
import type { GateResult } from "@/lib/api/types";
import { buildGateRows, type GateCatalogEntry, type GateRow } from "./gate-table";
import { modeOf, type GateDefinitionSource } from "./gate-cards";

export const catalogOf = (gates: readonly GateDefinitionSource[]): GateCatalogEntry[] =>
  gates.map((g) => ({ id: g.id, question: g.question, improves: g.improves, mode: modeOf(g.mode), grader: g.grader, notTriggered: g.not_triggered, message: g.message, impact: g.impact }));

export function episodeGateRows(episodeId: string, results: readonly GateResult[], gates: readonly GateDefinitionSource[]): GateRow[] {
  return buildGateRows(
    [
      {
        episode: { id: episodeId, label: "This episode", accountId: "" },
        results,
        spanTimes: new Map(),
        resolve: (ref) => ({ ref, kind: "ref", id: ref, label: ref, summary: null, spanId: null, href: null }),
      },
    ],
    catalogOf(gates),
  );
}
