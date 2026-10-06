// Moving through the graph: activities in time order for the arrow keys, and the trail of visited nodes.
import { isActivityType } from "./kinds";
import type { ExplorerNode } from "./model";

export const TRAIL_MAX = 6;

/** Activity ids, oldest first; ties by id; activities with no known time last. */
export function chronologicalActivities(nodes: readonly ExplorerNode[]): string[] {
  return nodes
    .filter((n) => isActivityType(n.type))
    .sort((a, b) => {
      if (a.at === null || b.at === null) return Number(a.at === null) - Number(b.at === null) || a.id.localeCompare(b.id);
      return a.at - b.at || a.id.localeCompare(b.id);
    })
    .map((n) => n.id);
}

/**
 * The activity one step from `current` (+1 later, -1 earlier). Stops at either end. From a node that is not
 * an activity, forward starts at the oldest and back at the newest. Null when there are no activities.
 */
export function stepThrough(order: readonly string[], current: string | null, dir: 1 | -1): string | null {
  if (order.length === 0) return null;
  const at = current === null ? -1 : order.indexOf(current);
  if (at < 0) return dir === 1 ? order[0]! : order[order.length - 1]!;
  return order[Math.min(order.length - 1, Math.max(0, at + dir))]!;
}

/** The trail with `id` as its newest entry: a revisited node moves to the end; the oldest falls off. */
export function pushTrail(trail: readonly string[], id: string): readonly string[] {
  if (trail[trail.length - 1] === id) return trail;
  return [...trail.filter((t) => t !== id), id].slice(-TRAIL_MAX);
}
