import type { GraphChange } from "@/lib/api/types";
import { shortId } from "@/lib/format";
import type { DiffIndex } from "@/lib/view/diff";
import { edgeLabel } from "@/lib/graph/kinds";
import { humanValue, kindTag } from "@/lib/graph/labels";

function changedProps(change: GraphChange): string[] {
  return Object.entries(change.changed ?? {}).map(([key, v]) => `${key}: ${humanValue(String(v.before))} → ${humanValue(String(v.after))}`);
}

export function DiffSummary({ marks, labels }: { marks: DiffIndex; labels: ReadonlyMap<string, string> }) {
  if (!marks.projected) return <p className="empty">Graph change not recorded: this event is not projected into the graph yet.</p>;
  const name = (id: string | undefined): string => (id ? (labels.get(id) ?? shortId(id)) : "?");
  const changed = marks.changes.filter((c) => c.op === "changed");
  return (
    <div data-testid="diff-summary">
      <p className="counts">
        <span className="count added">{marks.summary.added} added</span>
        <span className="count changed">{marks.summary.changed} changed</span>
        <span className="count removed">{marks.summary.removed} removed</span>
      </p>
      {changed.length > 0 ? (
        <ul>
          {changed.map((c) => (
            <li key={`${c.kind}:${c.id}`}>
              {name(c.id)} · {changedProps(c).join("; ")}
            </li>
          ))}
        </ul>
      ) : null}
      {marks.removed.length > 0 ? (
        <>
          <h4>Removed</h4>
          <ul>
            {marks.removed.map((c) => (
              <li key={`${c.kind}:${c.id}`}>{c.kind === "edge" ? `${edgeLabel(c.type)}: ${name(c.from)} → ${name(c.to)}` : `${name(c.id)} (${kindTag(c.type)})`}</li>
            ))}
          </ul>
        </>
      ) : null}
    </div>
  );
}
