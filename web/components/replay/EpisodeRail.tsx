import Link from "next/link";
import type { EpisodeReplayView } from "@/lib/api/types";
import { formatUtc } from "@/lib/format";
import { railItems, replayHref, windowFor, type RailItem } from "@/lib/view/replay";
import { EpisodeBadges, WindowTag } from "./EpisodeBadges";

interface Props {
  view: EpisodeReplayView;
  manifestId: string;
}

function provenance(event: NonNullable<RailItem["event"]>): string {
  return [event.provenance, event.provenance_origin].filter((p): p is string => typeof p === "string" && p.length > 0).join(" · ");
}

function ReleasedItem({ item, manifestId }: { item: RailItem & { event: NonNullable<RailItem["event"]> }; manifestId: string }) {
  const { event } = item;
  return (
    <Link href={replayHref(manifestId, event.position)} aria-current={item.selected ? "page" : undefined}>
      <span className="ep-head">
        <span className="ep-pos">#{event.position}</span>
        <span className="when">{formatUtc(event.occurred_at)}</span>
        <span className="ep-src">{event.source_system}</span>
      </span>
      <span className="ep-badges">
        <EpisodeBadges event={event} />
      </span>
      {provenance(event) ? <span className="hint ep-prov">{provenance(event)}</span> : null}
    </Link>
  );
}

/** The withheld next event: only what the boundary legitimately knows — never a consequence field. */
function WithheldItem({ item, boundary }: { item: RailItem & { event: NonNullable<RailItem["event"]> }; boundary: EpisodeReplayView["boundary"] }) {
  const { event } = item;
  return (
    <div className="ep-withheld-body" data-testid="withheld-card">
      <span className="ep-head">
        <span className="ep-pos">#{event.position}</span>
        <span className="when">{formatUtc(event.occurred_at)}</span>
        <span className="ep-src">{event.source_system}</span>
        <WindowTag boundary={boundary} position={event.position} />
      </span>
      <span className="ep-badges">
        <EpisodeBadges event={event} />
      </span>
      {provenance(event) ? <span className="hint ep-prov">{provenance(event)}</span> : null}
      <p className="withheld-note">Consequences are hidden until this event is released — no materiality, state change or graph diff is readable here.</p>
    </div>
  );
}

/**
 * The chronological rail of a replay: the boundary marker, released episodes (navigable via `?at`), the
 * withheld next event and unnamed future slots. Positions the view does not name are never invented.
 */
export function EpisodeRail({ view, manifestId }: Props) {
  return (
    <ol className="episodes">
      {railItems(view).map((item) => {
        const window = item.position > 0 ? windowFor(view.boundary, item.position) : "none";
        return (
          <li key={item.position} className={`ep ep-${item.state}${item.selected ? " selected" : ""} ep-w-${window}`} data-position={item.position}>
            {item.state === "boundary" ? (
              <Link href={replayHref(manifestId, 0)} aria-current={item.selected ? "page" : undefined}>
                <span className="ep-head">
                  <span className="ep-pos">#0</span>
                  <span>Before the first event</span>
                </span>
                <span className="hint">No episode released at this boundary.</span>
              </Link>
            ) : null}
            {item.state === "released" && item.event ? <ReleasedItem item={item as RailItem & { event: NonNullable<RailItem["event"]> }} manifestId={manifestId} /> : null}
            {item.state === "withheld" && item.event ? <WithheldItem item={item as RailItem & { event: NonNullable<RailItem["event"]> }} boundary={view.boundary} /> : null}
            {item.state === "future" ? (
              <div className="ep-future-body">
                <span className="ep-head">
                  <span className="ep-pos">#{item.position}</span>
                  {/* The configured boundary is legitimately known: a slot in the live window is held out. */}
                  <span>Not yet reached{window === "live" ? " (held-out window)" : ""}</span>
                  <WindowTag boundary={view.boundary} position={item.position} />
                </span>
                <span className="hint">This event is not even named at this boundary.</span>
              </div>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
