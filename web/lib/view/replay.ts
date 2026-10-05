// The episode-replay view model (HAR-129 section B/C): pure derivations over the contract view, so the
// leakage rules are testable without a render. Nothing here invents data — a position the view does not
// name stays unnamed ("future"), and an unreleased event is always "withheld".
import type { EpisodeEvent, EpisodeReplayView } from "@/lib/api/types";

/** The materiality verdict a released episode carries, or its withheld state when not yet released. */
export type EpisodeKind = "material" | "no_action" | "withheld";

export interface EpisodeBadge {
  kind: EpisodeKind;
  label: string;
  /** The trigger evaluation's reason for a non-material episode ("No action required" detail). */
  reason: string | null;
}

/**
 * The badge of one episode event. A released material episode is "Material"; a released non-material one
 * is "No action required" and carries the evaluation's reason; an unreleased event is "Withheld" — its
 * consequences are hidden and every consequence field is null in the payload.
 */
export function badgeFor(event: EpisodeEvent): EpisodeBadge {
  if (!event.released) return { kind: "withheld", label: "Withheld", reason: null };
  if (event.material === true) return { kind: "material", label: "Material", reason: null };
  return { kind: "no_action", label: "No action required", reason: event.no_action_reason };
}

export type ReplayWindow = EpisodeReplayView["window"];

/**
 * Which configured window a position falls in (section B): the historical-learning window
 * 1..historical_end and the held-out live window live_start..N. Position 0 — before the first event —
 * is in no window. Positions outside both windows (possible only with a broken boundary) are "none".
 */
export function windowFor(boundary: EpisodeReplayView["boundary"], position: number): ReplayWindow {
  if (position <= 0) return "none";
  if (position >= boundary.historical_start && position <= boundary.historical_end) return "historical";
  if (position >= boundary.live_start && position <= boundary.live_end) return "live";
  return "none";
}

/** Display text of a window tag. */
export function windowLabel(window: ReplayWindow): string {
  switch (window) {
    case "historical":
      return "historical";
    case "live":
      return "live";
    default:
      return "pre-release";
  }
}

export type RailState = "boundary" | "released" | "withheld" | "future";

export interface RailItem {
  /** The manifest position; 0 is the marker before the first event. */
  position: number;
  state: RailState;
  /** The episode event for released/withheld items; null for the boundary and for unnamed future slots. */
  event: EpisodeEvent | null;
  /** The position whose boundary the detail pane shows (view.episode). */
  selected: boolean;
}

/**
 * The episode rail: position 0 ("before the first event"), the released episodes 1..k, the withheld
 * next event k+1 (its identity is known at this bound, its consequences are not), then unnamed future
 * slots k+2..N — the boundary knows nothing of them, not even which one is held out.
 */
export function railItems(view: EpisodeReplayView): RailItem[] {
  const items: RailItem[] = [{ position: 0, state: "boundary", event: null, selected: view.episode === 0 }];
  const byPosition = new Map(view.prior_episodes.map((e) => [e.position, e]));
  for (let p = 1; p <= view.total; p += 1) {
    const released = byPosition.get(p);
    if (released) {
      items.push({ position: p, state: "released", event: released, selected: p === view.episode });
    } else if (view.next_event !== null && p === view.next_event.position) {
      items.push({ position: p, state: "withheld", event: view.next_event, selected: false });
    } else {
      items.push({ position: p, state: "future", event: null, selected: false });
    }
  }
  return items;
}

/** The released episode shown in the detail pane; null at the k=0 boundary (nothing released yet). */
export function boundaryEpisode(view: EpisodeReplayView): EpisodeEvent | null {
  return view.prior_episodes.find((e) => e.position === view.episode) ?? null;
}

export interface EpisodeNav {
  /** `?at` target for an earlier released position; null at the first boundary. */
  prev: number | null;
  /** `?at` target for a later released position; only while reviewing an earlier boundary. */
  next: number | null;
  /** Whether a "back to latest" link is needed (a `?at` view is being read). */
  latest: boolean;
}

/**
 * Previous/Forward navigation over released positions. At the live cursor only Previous exists
 * (Forward would cross the withheld event — that is what Play next does). Reviewing `?at=k` adds
 * Forward to k+1 — but never past the released cursor, so no Forward link points at a position
 * the core would refuse. `released` is the live cursor read alongside the `?at` view.
 */
export function navFor(view: EpisodeReplayView, at: number | null, released: number): EpisodeNav {
  const k = view.episode;
  const prev = k > 0 ? k - 1 : null;
  if (at === null || at >= released) return { prev, next: null, latest: at !== null };
  return { prev, next: k + 1 <= released ? k + 1 : null, latest: true };
}

/** The URL of a boundary view: the live cursor without `at`, an earlier released position with it. */
export function replayHref(manifestId: string, at: number | null): string {
  return at === null ? `/replay/${manifestId}` : `/replay/${manifestId}?at=${at}`;
}

export interface ParsedAt {
  at: number | null;
  malformed: boolean;
}

/**
 * The `?at=` query value: an integer position or absent. A malformed value is not a core call —
 * the loader shows the cursor with a notice instead of surfacing a core 4xx.
 */
export function parseAt(raw: string | undefined): ParsedAt {
  if (raw === undefined || raw === "") return { at: null, malformed: false };
  if (!/^\d+$/.test(raw)) return { at: null, malformed: true };
  const n = Number(raw);
  if (!Number.isSafeInteger(n) || n > 2_147_483_647) return { at: null, malformed: true };
  return { at: n, malformed: false };
}
