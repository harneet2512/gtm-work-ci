"use client";

import type { Activity } from "@/lib/api/types";
import { formatDay, formatUtc } from "@/lib/format";
import { activityTitle } from "@/lib/graph/labels";
import { nodeSelection, type Selection } from "@/lib/view/provenance";

interface Props {
  /** Already in time order (oldest first) and already cut off. */
  activities: readonly Activity[];
  cutoff: string | null;
  eventActivityIds: ReadonlySet<string>;
  selectedId: string | null;
  onSelect: (selection: Selection) => void;
}

/** "Meeting with Priya · Sep 25, 2026": the same name the graph gives this activity. */
const titleOf = (activity: Activity): string => `${activityTitle(activity.activity_type, activity)} · ${formatDay(activity.occurred_at)}`;

function activitySelection(activity: Activity): Selection {
  const selection = nodeSelection({
    id: activity.id,
    type: "Activity",
    label: activity.activity_type,
    valid_from: activity.occurred_at,
    evidence_refs: [{ activity_id: activity.id }],
    source_event_ids: [activity.source_event_id],
  });
  return { ...selection, title: titleOf(activity), meta: `Activity · ${selection.meta}` };
}

export function Timeline({ activities, cutoff, eventActivityIds, selectedId, onSelect }: Props) {
  return (
    <div>
      {cutoff ? <p className="hint">History through {formatUtc(cutoff)} (N-1), oldest first.</p> : <p className="hint">All activities, oldest first.</p>}
      {activities.length === 0 ? (
        <p className="empty">No activities in this window.</p>
      ) : (
        <ol className="timeline">
          {activities.map((activity) => (
            <li key={activity.id} className={eventActivityIds.has(activity.id) ? "from-event" : undefined}>
              <button type="button" aria-pressed={selectedId === activity.id} onClick={() => onSelect(activitySelection(activity))}>
                <span className="when">{formatUtc(activity.occurred_at)}</span>
                <strong>{activityTitle(activity.activity_type, activity)}</strong>
                {eventActivityIds.has(activity.id) ? <span className="flag">from this event</span> : null}
                <span className="summary">{activity.summary ?? activity.source_system}</span>
              </button>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
