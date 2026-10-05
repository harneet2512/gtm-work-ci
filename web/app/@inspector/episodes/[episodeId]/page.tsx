import { firstParam } from "@/lib/params";
import { getEpisodePage } from "@/lib/cached-loaders";
import { NODE_MARK } from "@/components/episode/EpisodeBody";
import { NODE_WORD } from "@/lib/view/episode";

export const dynamic = "force-dynamic";

type Params = Promise<{ episodeId: string }>;
type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * The episode inspector (HAR-145): `?node=` selects a trajectory node — its verdict, story line,
 * technical detail and the verbatim payload it rests on. No node selected → the frame stays a hint.
 */
export default async function EpisodeInspector({ params, searchParams }: { params: Params; searchParams: Search }) {
  const [{ episodeId }, sp] = await Promise.all([params, searchParams]);
  const nodeId = firstParam(sp.node) ?? null;
  if (!nodeId) {
    return (
      <div className="inspector-pane">
        <h2>Inspector</h2>
        <p className="hint">Select a node in the trajectory to inspect its evidence and payload.</p>
      </div>
    );
  }

  const data = await getEpisodePage(episodeId, firstParam(sp.manifest) ?? null);
  const node = data?.view.nodes.find((n) => n.id === nodeId) ?? null;
  if (!node) {
    return (
      <div className="inspector-pane">
        <h2>Inspector</h2>
        <p className="hint">No node “{nodeId}” on this episode's trajectory.</p>
      </div>
    );
  }

  return (
    <div className="inspector-pane">
      <h2>{node.label}</h2>
      <p className={`insp-status st-${node.status}`}>
        <span aria-hidden="true">{NODE_MARK[node.status]}</span> {NODE_WORD[node.status]}
      </p>
      <p>{node.summary}</p>
      {node.detail ? <p className="mono hint">{node.detail}</p> : null}
      {node.data != null ? (
        <details open>
          <summary>Payload</summary>
          <pre>{JSON.stringify(node.data, null, 2)}</pre>
        </details>
      ) : (
        <p className="hint">No payload behind this node.</p>
      )}
    </div>
  );
}
