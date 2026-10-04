import type { AccountSnapshot as Snapshot } from "@/lib/view/account-snapshot";
import { formatUtc, shortId } from "@/lib/format";

/**
 * Commitments, the next milestone and the latest agent action (WP24) — read straight off the
 * account-state contract fields (current_commitments, next_milestone) and the timeline's
 * AgentAction* activity types. Unknown fields say "unknown", never a fabricated value.
 */
export function AccountSnapshotView({ snapshot }: { snapshot: Snapshot }) {
  return (
    <section aria-labelledby="snap-h" className="panel snap-panel">
      <h2 id="snap-h">Commitments &amp; next step</h2>
      <dl className="kv">
        <dt>Commitments</dt>
        <dd>
          {snapshot.commitments === null ? (
            <span className="empty">unknown</span>
          ) : snapshot.commitments.length === 0 ? (
            <span className="empty">none recorded</span>
          ) : (
            <ul className="eval-diffs">
              {snapshot.commitments.map((c, i) => (
                <li key={i}>
                  {c.text}
                  {c.status ? <span className="badge"> {c.status}</span> : null}
                  {c.dueAt ? <span className="hint"> · due {formatUtc(c.dueAt)}</span> : null}
                </li>
              ))}
            </ul>
          )}
        </dd>
        <dt>Next milestone</dt>
        <dd>{snapshot.nextMilestone ?? <span className="empty">unknown</span>}</dd>
        <dt>Latest agent action</dt>
        <dd>
          {snapshot.latestAgentAction ? (
            <>
              <span className="badge">{snapshot.latestAgentAction.activity_type}</span> {snapshot.latestAgentAction.summary ?? snapshot.latestAgentAction.source_object_id}{" "}
              <span className="when">{formatUtc(snapshot.latestAgentAction.occurred_at)}</span>{" "}
              <code title={snapshot.latestAgentAction.id}>{shortId(snapshot.latestAgentAction.id)}</code>
            </>
          ) : (
            <span className="empty">No agent action in the loaded timeline window.</span>
          )}
        </dd>
      </dl>
    </section>
  );
}
