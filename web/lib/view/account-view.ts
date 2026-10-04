import type { Activity, Graph, GraphDiff } from "@/lib/api/types";
import { indexDiff, type DiffIndex } from "./diff";
import { timelineUpTo } from "./timeline";

export type PlayView = "before" | "after";

export interface AccountViewInput {
  /** The graph the core returned for this view: Before Play is a world read strictly before N, not the current graph. */
  graph: Graph;
  diff: GraphDiff | null;
  activities: readonly Activity[];
  view: PlayView;
  /** The resolved cutoff (occurred_at(N) or an explicit one); null when the view is not cut off. */
  cutoff: string | null;
  eventId: string | null;
}

export interface AccountView {
  view: PlayView;
  graph: Graph;
  marks: DiffIndex;
  /** Highlights are shown only After Play, for an event whose projection has been recorded. */
  showMarks: boolean;
  timeline: Activity[];
  cutoff: string | null;
  /** Activities that came from the event: flagged in the After Play timeline. */
  eventActivityIds: ReadonlySet<string>;
}

export const parseView = (raw: string | undefined): PlayView => (raw === "after" ? "after" : "before");

export function buildAccountView(input: AccountViewInput): AccountView {
  const marks = indexDiff(input.eventId === null ? null : input.diff);
  if (input.view === "before") {
    // The graph is already the world read before N: nothing is derived here from the After graph.
    return {
      view: "before",
      graph: input.graph,
      marks,
      showMarks: false,
      timeline: timelineUpTo(input.activities, input.cutoff),
      cutoff: input.cutoff,
      eventActivityIds: new Set(),
    };
  }
  const eventActivityIds = new Set(
    input.eventId === null ? [] : input.activities.filter((a) => a.source_event_id === input.eventId).map((a) => a.id),
  );
  return {
    view: "after",
    graph: input.graph,
    marks,
    showMarks: marks.projected,
    timeline: timelineUpTo(input.activities, null),
    cutoff: input.cutoff,
    eventActivityIds,
  };
}
