// The offline view (HAR-149 section 5): the checks that run on frozen cases to prove the system and its graders reliable, not on each
// customer request. Today none of them has run. Each is shown with what it measures and "Not run yet", and the recorded owner
// deviations (reference answers by a stronger model, a single trial) are said where they apply. No number appears here.
import type { GateDefinitionSource } from "@/lib/evals/gate-cards";

export interface Deviation {
  id: string;
  decision: string;
  applies_to: readonly string[];
}

export interface OfflineItem {
  id: string;
  question: string;
  name: string;
  /** What it measures, in plain words. */
  measures: string;
  trigger: string | null;
  status: "not_run" | "recorded";
  statusLabel: string;
  deviation: string | null;
  /** The registry's own status line (what is built today). */
  statusNote: string | null;
}

export interface OfflineView {
  headline: string;
  items: OfflineItem[];
}

/** The sentences of a note that do not match `drop`, joined again; null when nothing is left. */
function sentencesWithout(note: string, drop: RegExp): string | null {
  const kept = note.split(/(?<=\.)\s+/).filter((s) => s.trim() && !drop.test(s));
  return kept.length > 0 ? kept.join(" ") : null;
}

export function buildOfflineView(gates: readonly GateDefinitionSource[], deviations: readonly Deviation[], judgeSnapshotRecorded: boolean): OfflineView {
  const items = gates
    .filter((g) => g.mode === "offline_benchmark")
    .map((g): OfflineItem => {
      const recorded = g.id === "S2" && judgeSnapshotRecorded;
      const deviation = deviations.filter((d) => d.applies_to.includes(g.id)).map((d) => d.decision).join(" ") || null;
      // The registry's own status line often restates the owner deviation; the deviation is shown on its own line, so the status
      // line keeps only what else it says.
      const statusNote = g.status_note ? sentencesWithout(g.status_note, /owner deviation/i) : null;
      return {
        id: g.id,
        question: g.display_question ?? g.question,
        name: g.display_name ?? g.name,
        measures: g.plain_what ?? g.display_question ?? g.question,
        trigger: g.trigger ?? null,
        status: recorded ? "recorded" : "not_run",
        statusLabel: recorded ? "A judge-quality snapshot is recorded" : "Not run yet",
        deviation,
        statusNote,
      };
    });
  const ran = items.filter((i) => i.status === "recorded").length;
  return {
    headline: ran === 0 ? "None of these has run yet. Nothing below is a measurement." : `${ran} of ${items.length} has a recorded measurement; the rest have not run yet.`,
    items,
  };
}
