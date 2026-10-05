import Link from "next/link";
import type { GraphDiff, RunTrace } from "@/lib/api/types";
import { episodeHref, NODE_WORD, type EpisodeMode, type EpisodeNode } from "@/lib/view/episode";
import { ScrollTable } from "@/components/ScrollTable";

/** Neutral glyphs: a check mark is reserved for real EvalResult verdicts, never for "the data exists". */
export const NODE_MARK: Record<EpisodeNode["status"], string> = {
  recorded: "●",
  absent: "—",
  not_observable: "?",
  waiting: "○",
};

/**
 * The causal chain rail: one row per node, status mark + word + the story line. Selection is a link to
 * ?node=<id> — URL state, so back/forward and sharing work and the inspector slot follows the same param.
 */
export function EpisodeRail({ nodes, episodeId, mode, selected, manifest = null }: { nodes: EpisodeNode[]; episodeId: string; mode: EpisodeMode; selected: string | null; manifest?: string | null }) {
  return (
    <ol className="episode-rail" aria-label="Causal trajectory">
      {nodes.map((n) => (
        <li key={n.id} id={`node-${n.id}`} className={`enode st-${n.status}${n.id === selected ? " selected" : ""}`}>
          <Link href={episodeHref(episodeId, { node: n.id, mode, manifest })} aria-current={n.id === selected ? "true" : undefined}>
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

type TraceActivity = NonNullable<RunTrace["trigger_activities"]>[number];

const ActRow = ({ a, role }: { a: TraceActivity; role: string }) => (
  <tr>
    <td className="mono">{a.id.slice(0, 13)}…</td>
    <td>{role}</td>
    <td>
      {a.source_system} {a.activity_type}
    </td>
    <td className="wrap">{a.summary ?? "—"}</td>
    <td className="mono">{a.occurred_at}</td>
  </tr>
);

/** Trace mode: the raw span table — trigger and correlated activities plus the context accesses. */
export function TraceBody({ trace }: { trace: RunTrace }) {
  const accesses = (trace.context_accesses ?? []) as { access_id: number; tool: string; items?: unknown[]; bytes?: number; truncated?: boolean }[];
  return (
    <section aria-label="Trace spans">
      <ScrollTable label="Trace spans table"><table className="dense">
        <thead>
          <tr>
            <th>Activity</th>
            <th>Role</th>
            <th>Type</th>
            <th>Summary</th>
            <th>At</th>
          </tr>
        </thead>
        <tbody>
          {(trace.trigger_activities ?? []).map((a) => (
            <ActRow key={a.id} a={a} role="trigger" />
          ))}
          {(trace.correlated_activities ?? []).map((a) => (
            <ActRow key={a.id} a={a} role="evidence" />
          ))}
        </tbody>
      </table></ScrollTable>
      {accesses.length > 0 ? (
        <ScrollTable label="Context accesses"><table className="dense">
          <thead>
            <tr>
              <th>Access</th>
              <th>Tool</th>
              <th>Items</th>
              <th>Bytes</th>
              <th>Truncated</th>
            </tr>
          </thead>
          <tbody>
            {accesses.map((a) => (
              <tr key={a.access_id}>
                <td className="mono">{a.access_id}</td>
                <td>{a.tool}</td>
                <td>{a.items?.length ?? 0}</td>
                <td>{a.bytes ?? "—"}</td>
                <td>{a.truncated ? "yes" : "no"}</td>
              </tr>
            ))}
          </tbody>
        </table></ScrollTable>
      ) : null}
    </section>
  );
}

type DiffChange = GraphDiff["changes"][number];

const OP_MARK: Record<string, string> = { added: "+", removed: "−", changed: "~", repaired: "⟲" };

/** Graph-diff mode: the event's projection diff — +/−/~/⟲ per element — plus the state-diff changes. */
export function GraphDiffBody({ diff, trace }: { diff: GraphDiff | null; trace: RunTrace }) {
  const stateChanges = (trace.state_diff?.changes ?? []) as { field?: string; before?: unknown; after?: unknown }[];
  return (
    <section aria-label="Graph diff">
      {diff ? (
        <>
          <p className="hint">
            projection diff · +{diff.summary.added} −{diff.summary.removed} ~{diff.summary.changed} ⟲{diff.summary.repaired}
          </p>
          <ScrollTable label="Projection diff"><table className="dense">
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
                  <td className="mono">{c.id.slice(0, 13)}…</td>
                </tr>
              ))}
            </tbody>
          </table></ScrollTable>
        </>
      ) : (
        <p className="hint">No graph projection diff recorded for this event.</p>
      )}
      {stateChanges.length > 0 ? (
        <ScrollTable label="State changes"><table className="dense">
          <thead>
            <tr>
              <th>State field</th>
              <th>Before</th>
              <th>After</th>
            </tr>
          </thead>
          <tbody>
            {stateChanges.map((c, i) => (
              <tr key={i}>
                <td className="mono">{c.field ?? "?"}</td>
                <td className="wrap mono">{JSON.stringify(c.before ?? null)}</td>
                <td className="wrap mono">{JSON.stringify(c.after ?? null)}</td>
              </tr>
            ))}
          </tbody>
        </table></ScrollTable>
      ) : null}
    </section>
  );
}

/** Raw mode: the payloads the trace rests on, verbatim — nothing editorialized. */
export function RawBody({ view, graphDiff, trace }: { view: { nodes: EpisodeNode[] }; graphDiff: GraphDiff | null; trace: RunTrace }) {
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
      <details>
        <summary>run trace (full)</summary>
        <pre>{JSON.stringify(trace, null, 2)}</pre>
      </details>
      {graphDiff ? (
        <details>
          <summary>graph diff (full)</summary>
          <pre>{JSON.stringify(graphDiff, null, 2)}</pre>
        </details>
      ) : null}
    </section>
  );
}
