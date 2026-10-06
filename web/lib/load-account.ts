import { UUID } from "@/lib/uuid";
import { isActivityType } from "@/lib/graph/kinds";
import type { CoreClient } from "@/lib/api/core-client";
import { CoreError, errorCode } from "@/lib/api/core-client";
import type { AccountState, Activity, GraphDiff } from "@/lib/api/types";
import { buildAccountView, parseView, type AccountView } from "@/lib/view/account-view";
import { addMicrosecond, parseCutoff } from "@/lib/view/timeline";
import { transitionBadge, type TransitionBadge } from "@/lib/view/transition";

export type AccountApi = Pick<CoreClient, "getAccountGraph" | "getEventGraphDiff" | "getAccountState" | "getTimeline">;

export interface AccountQuery {
  event?: string;
  cutoff?: string;
  view?: string;
}

export interface AccountPageData {
  accountName: string;
  eventId: string | null;
  view: AccountView;
  state: AccountState | null;
  /** After Play: the state as it stood just before the event, so a changed fact can say what it used to be. */
  stateBefore: AccountState | null;
  badge: TransitionBadge;
  /** Sections that could not be read: the page still renders what it has. */
  notices: string[];
}


/** Only the core's error code is shown: the raw message can carry internal detail (review LOW). */
const reason = (e: unknown): string => errorCode(e, e instanceof Error ? e.message : String(e));

async function attempt<T>(notices: string[], what: string, fn: () => Promise<T>, fallback: T): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    notices.push(`${what} could not be read (${reason(e)}).`);
    return fallback;
  }
}

function activityInstant(change: GraphDiff["changes"][number]): string | undefined {
  const props = (change.props ?? {}) as Record<string, unknown>;
  return typeof props.occurred_at === "string" ? props.occurred_at : undefined;
}

/**
 * The instant of the Play event, from its own activity in the event's graph diff, else from the newest
 * timeline page. Null when it cannot be placed in world time: the caller then fails closed rather than
 * show the current graph labelled "Before Play" (H2).
 */
async function eventCutoff(api: AccountApi, accountId: string, eventId: string, diff: GraphDiff): Promise<string | null> {
  let best: string | null = null;
  let bestMs = Infinity;
  const consider = (at: string | undefined): void => {
    if (!at) return;
    const ms = Date.parse(at);
    if (!Number.isNaN(ms) && ms < bestMs) {
      bestMs = ms;
      best = at;
    }
  };
  for (const change of diff.changes) {
    // The core types an email or a call "Conversation" (it also carries the Activity label).
    if (change.kind === "node" && isActivityType(change.type) && change.source_event_ids.includes(eventId)) {
      consider(activityInstant(change));
    }
  }
  if (best !== null) return best;
  const activities = await api.getTimeline(accountId, { maxPages: 1 });
  for (const a of activities) if (a.source_event_id === eventId) consider(a.occurred_at);
  return best;
}

/**
 * Reads the endpoints one account page renders. The graph is required (a failure propagates to the error
 * page). For an event the graph diff is required too (fail closed). State, timeline and badge degrade to
 * a notice so the map stays usable; the state and graph are world-time reads (ADR-0019):
 * Before = `occurred_at(N)`, After = `occurred_at(N) + 1µs` (inclusive of N).
 */
export async function loadAccountPage(api: AccountApi, accountId: string, query: AccountQuery): Promise<AccountPageData> {
  const notices: string[] = [];
  const eventId = query.event === undefined || query.event === "" ? null : UUID.test(query.event) ? query.event : null;
  if (query.event && eventId === null) notices.push("The event id in the URL is not a uuid, so it was ignored.");
  const explicit = parseCutoff(query.cutoff);
  if (query.cutoff && explicit === null) notices.push("The cutoff in the URL is not a valid time, so it was ignored.");
  const mode = parseView(query.view);

  // The diff is required for an event: it places N in time and proves what changed. A failure is an error.
  const diff = eventId === null ? null : await api.getEventGraphDiff(eventId);
  let cutoff = explicit;
  if (eventId !== null && cutoff === null && diff !== null) cutoff = await eventCutoff(api, accountId, eventId, diff);
  if (eventId !== null && cutoff === null) {
    throw new CoreError(500, "event_time_unknown", `no activity of event ${eventId} could be placed in world time`);
  }

  const after = mode === "after";
  const worldAsOf = cutoff === null ? undefined : after ? addMicrosecond(cutoff) : cutoff;
  const timelineBefore = worldAsOf;

  const graph = await api.getAccountGraph(accountId, { limit: 20, worldAsOf });
  const priorAt = after && eventId !== null ? cutoff : null;
  const [activities, state, stateBefore] = await Promise.all([
    attempt<Activity[]>(notices, "The timeline", () => api.getTimeline(accountId, { before: timelineBefore }), []),
    attempt<AccountState | null>(notices, "The account state", () => api.getAccountState(accountId, { worldAsOf }), null),
    priorAt === null ? Promise.resolve(null) : attempt<AccountState | null>(notices, "The account state before the event", () => api.getAccountState(accountId, { worldAsOf: priorAt }), null),
  ]);

  const view = buildAccountView({ graph, diff, activities, view: mode, cutoff, eventId });
  const accountNode = graph.nodes.find((n) => n.type === "Account");
  return {
    accountName: state?.account_name ?? accountNode?.label ?? accountId,
    eventId,
    view,
    state,
    stateBefore,
    badge: transitionBadge(state),
    notices,
  };
}
