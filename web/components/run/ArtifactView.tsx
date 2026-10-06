import type { FinishedArtifact, Recipient } from "@/lib/api/types";
import { shortId } from "@/lib/format";

function RecipientLine({ label, list }: { label: string; list: readonly Recipient[] | null | undefined }) {
  if (!list || list.length === 0) return null;
  return (
    <p className="hint">
      {label}:{" "}
      {list.map((r, i) => (
        <span key={r.person_id}>
          {i > 0 ? ", " : ""}
          <code title={r.person_id}>{shortId(r.person_id)}</code>
          {r.why ? ` (${r.why})` : ""}
        </span>
      ))}
    </p>
  );
}

/**
 * A finished action artifact (the email/message as drafted, edited or sent) with its recipients —
 * the same shape candidates, edits and the final send all carry (finished_artifact.v1).
 */
export function ArtifactView({ artifact, to, cc, heading }: { artifact: FinishedArtifact | null | undefined; to?: readonly Recipient[] | null; cc?: readonly Recipient[] | null; heading?: string }) {
  if (!artifact && (to ?? []).length === 0 && (cc ?? []).length === 0) return <p className="empty">No artifact recorded.</p>;
  return (
    <div className="artifact">
      {heading ? <h4>{heading}</h4> : null}
      <RecipientLine label="to" list={to} />
      <RecipientLine label="cc" list={cc} />
      {artifact ? (
        <>
          {artifact.channel ? <p className="hint">channel: {artifact.channel}</p> : null}
          {artifact.subject ? <p className="summary">Subject: {artifact.subject}</p> : null}
          {artifact.body ? <blockquote className="artifact-body">{artifact.body}</blockquote> : null}
          {artifact.attachments && artifact.attachments.length > 0 ? <p className="hint">attachments: {artifact.attachments.join(", ")}</p> : null}
        </>
      ) : null}
    </div>
  );
}
