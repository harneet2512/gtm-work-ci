import type { DependencyInvalidation } from "@/lib/api/types";
import { buildRecomputationView, type RefView } from "@/lib/view/recomputation";

function Bucket({ title, refs, note }: { title: string; refs: RefView[]; note?: string }) {
  return (
    <section className="recompute-bucket">
      <h4>{title}</h4>
      {note ? <p className="hint">{note}</p> : null}
      {refs.length === 0 ? (
        <p className="hint">None</p>
      ) : (
        <ul>
          {refs.map((r) => (
            <li key={r.key}>
              <span className="rlabel">{r.label}</span>
              {r.verdict ? <span className="tag"> {r.verdict}</span> : null}
              <span className="hint"> {r.reason}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/**
 * What the human's edits invalidated, re-evaluated, did not recompute and preserved (HAR-145), as the core derived it.
 * `unavailable` is a failed read (never "nothing was edited"); a null document with no failure renders nothing.
 */
export function Recomputation({ recomputation, unavailable = false }: { recomputation: DependencyInvalidation | null; unavailable?: boolean }) {
  if (!recomputation) {
    return unavailable ? (
      <section className="panel recompute" aria-labelledby="recompute-h">
        <h2 id="recompute-h">What the edit changed</h2>
        <p className="hint">What the edit changed could not be read: backend unavailable.</p>
      </section>
    ) : null;
  }
  const v = buildRecomputationView(recomputation);
  return (
    <section className="panel recompute" aria-labelledby="recompute-h">
      <h2 id="recompute-h">What the edit changed</h2>
      <p>{v.headline}</p>
      {v.labels.length > 0 ? <p className="hint">Semantic change: {v.labels.join(", ")}</p> : null}
      <p className={`state-preserved is-${v.state.tone}`}>{v.state.text}</p>
      {v.entries.map((e) => (
        <div key={e.index} role="group" aria-label={`Edit ${e.index + 1}: ${e.edit}`} className="recompute-entry">
          <h3>
            Edit {e.index + 1}: {e.edit}
          </h3>
          <div className="recompute-grid">
            <Bucket title="Invalidated" refs={e.invalidated} />
            <Bucket title="Re-evaluated" refs={e.reevaluated} />
            <Bucket title="Not recomputed" refs={e.notRecomputed} />
            <Bucket title="Preserved" refs={e.preserved} />
          </div>
        </div>
      ))}
    </section>
  );
}
