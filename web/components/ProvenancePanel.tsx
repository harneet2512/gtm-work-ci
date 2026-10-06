import type { Activity } from "@/lib/api/types";
import { formatDay, formatUtc } from "@/lib/format";
import { activityTitle, humanizeKey } from "@/lib/graph/labels";
import type { Provenance, ProvenanceItem } from "@/lib/view/provenance";

const OUTRANKED = "Outranked, retained";

function speakerName(activity: Activity | undefined, personId: string | undefined): string | undefined {
  if (!personId) return undefined;
  return activity?.participants.find((p) => p.person_id === personId)?.display_name ?? personId;
}

const SOURCES: Readonly<Record<string, string>> = { email: "Email", crm: "CRM record", slack: "Slack", docs: "Document", calendar: "Calendar", call: "Call" };
const sourceName = (system: string): string => SOURCES[system] ?? humanizeKey(system);

/** One piece of evidence: what happened, the quote, and chips for who said it, the source record and the standing. */
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
        <strong>{activityTitle(activity.activity_type, activity)}</strong> <span className="when">{formatUtc(activity.occurred_at)}</span>
      </p>
      {activity.summary ? <p>{activity.summary}</p> : null}
      {item.quote ? <blockquote>{item.quote}</blockquote> : null}
      <ul className="chips" aria-label="Evidence details">
        {speaker ? <li className="chip">{`Said by ${speaker}`}</li> : null}
        <li className="chip">{`${sourceName(activity.source_system)} · ${formatDay(activity.occurred_at)}`}</li>
        {item.standing ? <li className={item.standing === OUTRANKED ? "chip chip-warn" : "chip"}>{item.standing}</li> : null}
      </ul>
      <details className="source-details">
        <summary>Source details</summary>
        <p className="hint">{`${activity.source_system} · ${activity.source_object_id}`}</p>
        {item.claimId ? <p className="hint">claim {item.claimId}</p> : null}
      </details>
    </li>
  );
}

function Facts({ facts }: { facts: Provenance["facts"] }) {
  if (facts.length === 0) return null;
  return (
    <dl className="prov-facts">
      {facts.map((f) => (
        <div key={f.label} data-fact={f.label}>
          <dt>{f.label}</dt>
          <dd className={f.value === OUTRANKED ? "chip chip-warn" : undefined}>{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Related({ related, onOpen }: { related: Provenance["related"]; onOpen?: (id: string) => void }) {
  if (related.length === 0 || !onOpen) return null;
  return (
    <section className="prov-related" aria-label="Connected nodes">
      <h4>{`Connected (${related.length})`}</h4>
      <ul>
        {related.map((r) => (
          <li key={`${r.direction}:${r.rel}:${r.id}`}>
            <button type="button" aria-label={`Open ${r.name}`} onClick={() => onOpen(r.id)}>
              <span className="prov-rel">{r.direction === "in" ? `← ${r.rel}` : `${r.rel} →`}</span>
              <span>{r.name}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function ProvenancePanel({ provenance, onOpen }: { provenance: Provenance | null; onOpen?: (id: string) => void }) {
  if (!provenance) {
    return <p className="empty">Click a node, edge, claim or activity to see the evidence it came from.</p>;
  }
  return (
    <div data-testid="provenance">
      <h3>{provenance.title}</h3>
      {provenance.meta ? <p className="hint">{provenance.meta}</p> : null}
      {provenance.withheld ? null : <Facts facts={provenance.facts} />}
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
      <Related related={provenance.related} onOpen={onOpen} />
    </div>
  );
}
