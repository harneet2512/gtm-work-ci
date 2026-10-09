"use client";

import { kindOf } from "@/lib/graph/kinds";
import type { DiffRow, DiffRows } from "@/lib/graph/transition";
import { ShapeGlyph } from "./GraphLegend";

interface Props {
  rows: DiffRows;
  onJump: (id: string) => void;
}

function RowContent({ row }: { row: DiffRow }) {
  return (
    <>
      {row.kind === "node" ? <ShapeGlyph shape={kindOf(row.lead).shape} /> : <span className="gx-rel">{row.lead}</span>}
      <span className="gx-row-body">{row.body}</span>
      {row.detail ? <span className="gx-row-detail">{row.detail}</span> : null}
    </>
  );
}

function Group({ title, tone, rows, onJump }: { title: string; tone: string; rows: readonly DiffRow[]; onJump: (id: string) => void }) {
  if (rows.length === 0) return null;
  return (
    <section className={`gx-diff-group tone-${tone}`} aria-label={title}>
      <h4>
        {title} <span className="gx-count">{rows.length}</span>
      </h4>
      <ul>
        {rows.map((row) => (
          <li key={row.key} data-kind={row.kind}>
            {row.focusId === null ? (
              <span className="gx-diff-row is-gone" title={row.text}>
                <RowContent row={row} />
              </span>
            ) : (
              <button type="button" className="gx-diff-row" title={row.text} aria-label={`Show ${row.text}`} onClick={() => onJump(row.focusId!)}>
                <RowContent row={row} />
              </button>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

/** What the Play event did to the graph, in a rail beside the canvas; each element jumps to its place in it. */
export function GraphDiffPanel({ rows, onJump }: Props) {
  const total = rows.added.length + rows.changed.length + rows.removed.length;
  return (
    <section className="gx-rail" aria-labelledby="gx-rail-h" data-testid="graph-diff">
      <h3 id="gx-rail-h">What this event changed</h3>
      {rows.recorded ? (
        <>
          {total === 0 ? <p className="gx-note">The event changed nothing in this graph.</p> : null}
          <Group title="Added" tone="added" rows={rows.added} onJump={onJump} />
          <Group title="Changed" tone="changed" rows={rows.changed} onJump={onJump} />
          <Group title="Removed" tone="removed" rows={rows.removed} onJump={onJump} />
          {rows.unattributed > 0 ? <p className="gx-note">{`${rows.unattributed} more changes landed in the same update but are not evidenced by this event.`}</p> : null}
        </>
      ) : (
        <p className="gx-note">{rows.note}</p>
      )}
    </section>
  );
}
