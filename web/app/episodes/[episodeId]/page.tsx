import Link from "next/link";
import { notFound } from "next/navigation";
import { firstParam } from "@/lib/params";
import { getEpisodePage } from "@/lib/cached-loaders";
import { parseMode, episodeHref, spanOfKind, EPISODE_MODES } from "@/lib/view/episode";
import { EpisodeRail, GraphDiffBody, RawBody, TraceBody, UnknownNodeNote } from "@/components/episode/EpisodeBody";
import { FocusNode } from "@/components/episode/FocusNode";
import { CliffBody } from "@/components/episode/CliffBody";
import { KnowledgeMutations } from "@/components/episode/KnowledgeMutations";
import { Recomputation } from "@/components/run/Recomputation";

export const dynamic = "force-dynamic";

type Params = Promise<{ episodeId: string }>;
type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * /episodes/[id]: one decision episode's causal trajectory (HAR-145), read from the episode summary and its trace. The
 * rail is the chain itself; Story is the default reading, Trace the span table, Graph diff the projection and state
 * changes, Raw the payloads. `?node=` selects a node for the inspector without leaving the page; `?mode=` switches views.
 */
export default async function EpisodePage({ params, searchParams }: { params: Params; searchParams: Search }) {
  const { episodeId } = await params;
  const sp = await searchParams;
  const mode = parseMode(firstParam(sp.mode));
  const selected = firstParam(sp.node) ?? null;

  const data = await getEpisodePage(episodeId);
  if (!data) notFound();
  const { view, graphDiff, trace } = data;
  const s = view.summary;

  return (
    <section className="page episode">
      <header className="episode-head">
        <div>
          <p className="eyebrow">Decision episode</p>
          <h1 className="mono">{episodeId.slice(0, 13)}…</h1>
        </div>
        <dl className="context-bar">
          <div>
            <dt>Account</dt>
            <dd>
              <Link href={`/accounts/${s.account_id}`}>{s.account_name}</Link>
            </dd>
          </div>
          <div>
            <dt>Run</dt>
            <dd>
              <Link href={`/runs/${s.run.id}`} className="mono">
                {s.run.id.slice(0, 13)}…
              </Link>
            </dd>
          </div>
          <div>
            <dt>Phase</dt>
            <dd>{s.run.phase ?? "—"}</dd>
          </div>
          <div>
            <dt>Status</dt>
            <dd>{s.final_status.replaceAll("_", " ")}</dd>
          </div>
        </dl>
        <nav className="toggle" aria-label="Trace modes">
          {EPISODE_MODES.map((m) =>
            m.id === mode ? (
              <span key={m.id} className="off" aria-current="page">
                {m.label}
              </span>
            ) : (
              <Link key={m.id} href={episodeHref(episodeId, { mode: m.id, node: selected })}>
                {m.label}
              </Link>
            ),
          )}
        </nav>
      </header>

      {data.notices.length > 0 ? (
        <ul className="notices" role="status">
          {data.notices.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}

      <div className="episode-grid">
        <EpisodeRail nodes={view.nodes} episodeId={episodeId} mode={mode} selected={selected} />
        <div className="episode-body">
          <UnknownNodeNote nodes={view.nodes} selected={selected} />
          <FocusNode nodeId={selected} />
          {mode === "trace" ? <TraceBody trace={trace} unassignedEvalCount={view.unassignedEvalCount} /> : null}
          {mode === "graphdiff" ? <GraphDiffBody diff={graphDiff} stateSpan={spanOfKind(trace, "state")} /> : null}
          {mode === "cliff" ? (
            <CliffBody surfaces={data.surfaces} readable={data.surfacesReadable} bi={data.bi} biStatus={data.biStatus} strategies={data.strategies} decision={data.decision} inference={data.inference} runId={s.run.id} />
          ) : null}
          {mode === "raw" ? <RawBody view={view} graphDiff={graphDiff} trace={trace} /> : null}
          {mode === "story" ? (
            <>
              <p className="hint">The rail is the story: select a node for its evidence in the inspector, or switch mode for the spans, diffs and raw payloads behind it.</p>
              <KnowledgeMutations mutations={data.mutations} />
              <Recomputation recomputation={data.recomputation} unavailable={data.notices.some((n) => n.startsWith("The edit recomputation"))} />
            </>
          ) : null}
        </div>
      </div>
    </section>
  );
}
