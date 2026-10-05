import Link from "next/link";
import type { EpisodeTrace, GraphDiff, TraceSpan } from "@/lib/api/types";
import { episodeHref, NODE_WORD, type EpisodeMode, type EpisodeNode } from "@/lib/view/episode";
import { ScrollTable } from "@/components/ScrollTable";

/** Neutral glyphs: a check mark is reserved for real EvalResult verdicts, never for "the data exists". */
export const NODE_MARK: Record<EpisodeNode["status"], string> = {
  recorded: "●",
  pending: "○",
  not_recorded: "—",
  not_measured: "?",
};

/**
 * The causal chain rail: one row per node, status mark + word + the story line. Selection is a link to
 * ?node=<id>: URL state, so back/forward and sharing work and the inspector slot follows the same param.
 */
export function EpisodeRail({ nodes, episodeId, mode, selected }: { nodes: EpisodeNode[]; episodeId: string; mode: EpisodeMode; selected: string | null }) {
  return (
    <ol className="episode-rail" aria-label="Causal trajectory">
      {nodes.map((n) => (
        <li key={n.id} id={`node-${n.id}`} className={`enode st-${n.status}${n.id === selected ? " selected" : ""}`}>
          <Link href={episodeHref(episodeId, { node: n.id, mode })} aria-current={n.id === selected ? "true" : undefined}>
            <span className="mark" aria-hidden="true">
              {NODE_MARK[n.status]}
            </span>
            <span className="sr-only">{NODE_WORD[n.status]}</span>
            <span className="nlabel">{n.label}</span>
            <span className="nsummary">{n.summary}</span>
          </Link>
        </li>
      ))}
    </ol>
  );
}

/** Shown when a deep link's ?node= names no node: the page still renders the whole trajectory. */
export function UnknownNodeNote({ nodes, selected }: { nodes: EpisodeNode[]; selected: string | null }) {
  if (!selected || nodes.some((n) => n.id === selected)) return null;
  return (
    <p className="hint" role="status">
      No node “{selected}” on this trajectory — select one from the rail.
    </p>
  );
}

const short = (id: string) => (id.length > 13 ? `${id.slice(0, 13)}…` : id);

const SpanRow = ({ s }: { s: TraceSpan }) => (
  <tr>
    <td className="mono">{s.seq}</td>
    <td>{s.title}</td>
    <td>
      <span className={`span-status st-${s.status}`}>{NODE_WORD[s.status]}</span>
    </td>
    <td className="mono">{s.occurred_at ?? "—"}</td>
    <td className="wrap">{s.refs.length === 0 ? "—" : s.refs.map((r) => `${r.kind.replaceAll("_", " ")} ${short(r.id)}`).join(" · ")}</td>
    <td>{s.eval_result_ids.length}</td>
  </tr>
);

/** Trace mode: the served spans in causal order, each with its status, the rows behind it and the eval results that judged it. */
export function TraceBody({ trace, unassignedEvalCount = 0 }: { trace: EpisodeTrace | null; unassignedEvalCount?: number }) {
  if (!trace) return <p className="hint">No trace is recorded for this episode.</p>;
  return (
    <section aria-label="Trace spans">
      <ScrollTable label="Trace spans table">
        <table className="dense">
          <thead>
            <tr>
              <th>#</th>
              <th>Span</th>
              <th>Status</th>
              <th>At</th>
              <th>Rows behind it</th>
              <th>Evals</th>
            </tr>
          </thead>
          <tbody>
            {[...trace.spans]
              .sort((a, b) => a.seq - b.seq)
              .map((s) => (
                <SpanRow key={s.id} s={s} />
              ))}
          </tbody>
        </table>
      </ScrollTable>
      {unassignedEvalCount > 0 ? (
        <p className="hint">
          {unassignedEvalCount} eval {unassignedEvalCount === 1 ? "result" : "results"} (validation and safety checks) belong to no single span.
        </p>
      ) : null}
    </section>
  );
}

type DiffChange = GraphDiff["changes"][number];

const OP_MARK: Record<string, string> = { added: "+", removed: "−", changed: "~", repaired: "⟲" };

const attr = (span: TraceSpan | null, key: string): unknown => (span?.attributes as Record<string, unknown> | undefined)?.[key];

/** Graph-diff mode: the event's projection diff (+/−/~/⟲ per element) and what the state span says changed. */
export function GraphDiffBody({ diff, stateSpan }: { diff: GraphDiff | null; stateSpan: TraceSpan | null }) {
  const fields = attr(stateSpan, "changed_fields");
  const changed = Array.isArray(fields) ? fields.map(String) : [];
  const from = attr(stateSpan, "from_version");
  const to = attr(stateSpan, "to_version");
  return (
    <section aria-label="Graph diff">
      {diff ? (
        <>
          <p className="hint">
            projection diff · +{diff.summary.added} −{diff.summary.removed} ~{diff.summary.changed} ⟲{diff.summary.repaired}
          </p>
          <ScrollTable label="Projection diff">
            <table className="dense">
              <thead>
                <tr>
                  <th>Op</th>
                  <th>Kind</th>
                  <th>Type</th>
                  <th>Id</th>
                </tr>
              </thead>
              <tbody>
                {diff.changes.map((c: DiffChange, i: number) => (
                  <tr key={`${c.id}-${i}`}>
                    <td className={`mono op-${c.op}`}>{OP_MARK[c.op] ?? c.op}</td>
                    <td>{c.kind}</td>
                    <td>{c.type}</td>
                    <td className="mono">{short(c.id)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </ScrollTable>
        </>
      ) : (
        <p className="hint">No graph projection diff recorded for this event.</p>
      )}
      {stateSpan && stateSpan.status === "recorded" ? (
        <div className="state-change">
          <p className="hint">{stateSpan.summary}</p>
          {typeof from === "number" && typeof to === "number" ? (
            <p className="mono">
              account state v{from} → v{to}
            </p>
          ) : null}
          {changed.length > 0 ? (
            <ul className="chips" aria-label="State fields that changed">
              {changed.map((f) => (
                <li key={f} className="mono">
                  {f}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : (
        <p className="hint">No account-state change is recorded for this episode.</p>
      )}
    </section>
  );
}

/** Raw mode: the spans the trace rests on, verbatim, plus the summary and diff payloads: nothing editorialized. */
export function RawBody({ view, graphDiff, trace }: { view: { nodes: EpisodeNode[] }; graphDiff: GraphDiff | null; trace: EpisodeTrace | null }) {
  return (
    <section aria-label="Raw payloads" className="raw">
      {view.nodes.map((n) => (
        <details key={n.id} id={`raw-${n.id}`}>
          <summary>
            <span className="mark" aria-hidden="true">
              {NODE_MARK[n.status]}
            </span>{" "}
            {n.label}
          </summary>
          <pre>{JSON.stringify(n.data ?? null, null, 2)}</pre>
        </details>
      ))}
      {trace ? (
        <details>
          <summary>episode trace (full)</summary>
          <pre>{JSON.stringify(trace, null, 2)}</pre>
        </details>
      ) : null}
      {graphDiff ? (
        <details>
          <summary>graph diff (full)</summary>
          <pre>{JSON.stringify(graphDiff, null, 2)}</pre>
        </details>
      ) : null}
    </section>
  );
}
