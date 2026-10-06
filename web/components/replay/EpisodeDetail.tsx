import Link from "next/link";
import type { EpisodeReplayView } from "@/lib/api/types";
import { formatUtc, shortId } from "@/lib/format";
import { episodeHref } from "@/lib/view/episode";
import { boundaryEpisode, windowLabel } from "@/lib/view/replay";
import { EpisodeBadges, WindowTag } from "./EpisodeBadges";

interface Props {
  view: EpisodeReplayView;
  /** The `?at` in the URL, or null at the live cursor. */
  at: number | null;
}

function IdValue({ label, id }: { label: string; id: string | null }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{id ? <code>{id}</code> : <span className="empty">none</span>}</dd>
    </>
  );
}

/**
 * The detail pane of the viewed boundary (episode k): the released episode's bookkeeping, the
 * world-timed account state at the episode bound, the knowledge applicable as of that moment and the
 * withheld next event — with what is hidden made explicit (HAR-129 §B).
 */
export function EpisodeDetail({ view, at }: Props) {
  const episode = boundaryEpisode(view);
  return (
    <div>
      <p className="hint">
        Episode {view.episode} of {view.total} · {windowLabel(view.window)} window · computed {formatUtc(view.computed_at)}
        {at !== null ? " · viewing an earlier released position (read-only)" : " · live cursor"}
      </p>

      {episode === null ? (
        <p className="empty">No event has been released at this boundary yet. The first event is withheld below.</p>
      ) : (
        <>
          <h3>
            Episode {episode.position} <EpisodeBadges event={episode} /> <WindowTag boundary={view.boundary} position={episode.position} />
          </h3>
          <dl className="kv">
            <dt>Occurred</dt>
            <dd>{formatUtc(episode.occurred_at)}</dd>
            <dt>Source system</dt>
            <dd>{episode.source_system}</dd>
            {episode.provenance || episode.provenance_origin ? (
              <>
                <dt>Provenance</dt>
                <dd>{[episode.provenance, episode.provenance_origin].filter(Boolean).join(" · ")}</dd>
              </>
            ) : null}
            <dt>Event</dt>
            <dd>
              <code>{episode.event_id}</code>
            </dd>
            <IdValue label="Account change" id={episode.account_change_id} />
            <dt>Decision episode</dt>
            <dd>
              {episode.decision_episode_id ? (
                <Link href={episodeHref(episode.decision_episode_id, { manifest: view.manifest_id })}>
                  <code>{episode.decision_episode_id}</code>
                </Link>
              ) : (
                <span className="empty">none</span>
              )}
            </dd>
            <dt>State version at bound</dt>
            <dd>{episode.state_version === null ? <span className="empty">none visible at this bound</span> : `v${episode.state_version}`}</dd>
            <dt>Graph diff</dt>
            <dd>
              {episode.graph_diff_id === null ? (
                <span className="empty">not recorded by the projector</span>
              ) : (
                <code title="graph_projection_diffs.id">diff #{episode.graph_diff_id}</code>
              )}
            </dd>
            {episode.no_action_reason ? (
              <>
                <dt>No-action reason</dt>
                <dd>{episode.no_action_reason}</dd>
              </>
            ) : null}
            {episode.coalesced === true ? (
              <>
                <dt>Coalesced</dt>
                <dd>This event was folded with others; the verdict above is the fold's shared verdict.</dd>
              </>
            ) : null}
          </dl>
        </>
      )}

      <h3>Account state at this bound</h3>
      {view.state === null ? (
        <p className="empty">{view.episode === 0 ? "No state exists before the first event." : "No account state was visible at this bound."}</p>
      ) : (
        <>
          <dl className="kv">
            <dt>Version</dt>
            <dd>v{view.state.version}</dd>
            <dt>As of</dt>
            <dd>{view.state.as_of ? formatUtc(view.state.as_of) : "unknown"}</dd>
            <dt>Digest</dt>
            <dd>
              <code className="digest">{view.state.digest}</code>
            </dd>
          </dl>
          <details className="state-doc">
            <summary>State document</summary>
            <pre>{JSON.stringify(view.state.document, null, 2)}</pre>
          </details>
        </>
      )}

      <h3>Knowledge applicable at this bound</h3>
      {view.knowledge.as_of ? <p className="hint">Replayed as of {formatUtc(view.knowledge.as_of)}.</p> : null}
      {view.knowledge.items.length === 0 ? (
        <p className="empty">No knowledge is applicable at this bound.</p>
      ) : (
        <ul className="knowledge">
          {view.knowledge.items.map((k) => (
            <li key={k.id}>
              <span className={`badge know-${k.status}`}>{k.status}</span> {k.title} <code>{shortId(k.id)}</code>
            </li>
          ))}
        </ul>
      )}

      {view.next_event ? (
        <div className="next-strip" data-testid="detail-next">
          <h3>Next event — withheld</h3>
          <p>
            #{view.next_event.position} · {formatUtc(view.next_event.occurred_at)} · {view.next_event.source_system}
            {view.next_event.held_out ? " · held-out" : ""}
          </p>
          <p className="withheld-note">Materiality, state change, graph diff and decision links are hidden until release.</p>
        </div>
      ) : (
        <p className="hint">Every event of this manifest is released; the replay is complete.</p>
      )}
    </div>
  );
}
