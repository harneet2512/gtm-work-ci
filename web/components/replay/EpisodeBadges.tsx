import type { EpisodeEvent, EpisodeReplayView } from "@/lib/api/types";
import { badgeFor, windowFor, windowLabel } from "@/lib/view/replay";

/**
 * The verdict badges of one episode event: material / no-action / withheld, plus the held-out and
 * coalesced-fold markers when the payload sets them. A coalesced badge means the materiality and
 * no-action reason are the shared fold's verdict, not a per-event evaluation.
 */
export function EpisodeBadges({ event }: { event: EpisodeEvent }) {
  const badge = badgeFor(event);
  return (
    <span className="badges" data-testid="episode-badges" data-kind={badge.kind}>
      <span className={`badge kind-${badge.kind}`}>{badge.label}</span>
      {event.held_out ? <span className="badge kind-held-out">held-out</span> : null}
      {event.coalesced === true ? <span className="badge kind-coalesced">coalesced fold</span> : null}
      {badge.reason ? <span className="badge reason">{badge.reason}</span> : null}
    </span>
  );
}

/** The historical/live window a position sits in (section B's configured boundary). */
export function WindowTag({ boundary, position }: { boundary: EpisodeReplayView["boundary"]; position: number }) {
  const window = windowFor(boundary, position);
  return (
    <span className={`badge window-${window}`} data-testid={`window-${position}`}>
      {windowLabel(window)}
    </span>
  );
}
