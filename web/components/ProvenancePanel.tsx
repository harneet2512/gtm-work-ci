import type { Activity } from "@/lib/api/types";
import { formatUtc } from "@/lib/format";
import type { Provenance, ProvenanceItem } from "@/lib/view/provenance";

function speakerName(activity: Activity | undefined, personId: string | undefined): string | undefined {
  if (!personId) return undefined;
  return activity?.participants.find((p) => p.person_id === personId)?.display_name ?? personId;
}

function Evidence({ item }: { item: ProvenanceItem }) {
  const { activity } = item;
  if (!activity) {
    return (
      <li className="evidence unresolved">
        <code>{item.activityId}</code>
        <p className="hint">This activity is not in the loaded timeline window.</p>
        {item.quote ? <blockquote>{item.quote}</blockquote> : null}
      </li>
    );
  }
  const speaker = speakerName(activity, item.speaker);
  return (
    <li className="evidence" data-activity-id={activity.id}>
      <p>
        <strong>{activity.activity_type}</strong> <span className="when">{formatUtc(activity.occurred_at)}</span>
      </p>
      {activity.summary ? <p>{activity.summary}</p> : null}
      {item.quote ? (
        <blockquote>
          {item.quote}
          {speaker ? <cite> {speaker}</cite> : null}
        </blockquote>
      ) : null}
      <p className="hint">{`${activity.source_system} · ${activity.source_object_id}`}</p>
      {item.claimId ? <p className="hint">claim {item.claimId}</p> : null}
    </li>
  );
}

export function ProvenancePanel({ provenance }: { provenance: Provenance | null }) {
  if (!provenance) {
    return <p className="empty">Click a node, edge, claim or activity to see the evidence it came from.</p>;
  }
  return (
    <div data-testid="provenance">
      <h3>{provenance.title}</h3>
      {provenance.meta ? <p className="hint">{provenance.meta}</p> : null}
      {provenance.withheld ? (
        <p className="empty">The evidence for this element is hidden by visibility rules.</p>
      ) : provenance.items.length === 0 ? (
        <p className="empty">No evidence is recorded for this element.</p>
      ) : (
        <ul className="evidence-list">
          {provenance.items.map((item, i) => (
            <Evidence key={`${item.activityId}:${i}`} item={item} />
          ))}
        </ul>
      )}
    </div>
  );
}
