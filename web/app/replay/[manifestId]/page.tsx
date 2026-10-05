import Link from "next/link";
import { notFound } from "next/navigation";
import { EpisodeDetail } from "@/components/replay/EpisodeDetail";
import { EpisodeRail } from "@/components/replay/EpisodeRail";
import { ReplayControls } from "@/components/replay/ReplayControls";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";
import { loadReplayPage } from "@/lib/load-replay";
import { firstParam } from "@/lib/params";
import { UUID } from "@/lib/uuid";
import { navFor, replayHref, windowLabel } from "@/lib/view/replay";
import { advanceEpisode, resetReplayCursor } from "./actions";

export const dynamic = "force-dynamic";

type Params = Promise<{ manifestId: string }>;
type Search = Promise<Record<string, string | string[] | undefined>>;

export default async function ReplayPage({ params, searchParams }: { params: Params; searchParams: Search }) {
  const { manifestId } = await params;
  const sp = await searchParams;
  if (!UUID.test(manifestId)) notFound();

  let data;
  try {
    data = await loadReplayPage(core(), manifestId, firstParam(sp.at));
  } catch (e) {
    if (e instanceof InvalidIdError || (e instanceof CoreError && e.status === 404)) notFound();
    throw e;
  }

  const { view } = data;
  const nav = navFor(view, data.at, data.released);
  const reviewing = data.at !== null && data.at < data.released;
  return (
    <section className="page">
      <div className="head">
        <h1>Episode replay</h1>
        <span className={`badge window-${view.window}`}>{view.window === "none" ? windowLabel("none") : `${view.window} window`}</span>
        <span className="hint">
          episode {view.episode} of {view.total}
        </span>
        <nav aria-label="Episode navigation" className="toggle">
          {nav.prev !== null ? (
            <Link href={replayHref(manifestId, nav.prev)}>‹ Previous</Link>
          ) : (
            <span className="off">‹ Previous</span>
          )}
          {nav.next !== null ? (
            <Link href={nav.next === data.released ? replayHref(manifestId, null) : replayHref(manifestId, nav.next)}>Forward ›</Link>
          ) : null}
          {nav.latest ? <Link href={replayHref(manifestId, null)}>Latest</Link> : null}
        </nav>
      </div>
      <p className="hint">
        manifest <code>{manifestId}</code> · account{" "}
        <Link href={`/accounts/${view.account_id}`}>
          <code>{view.account_id}</code>
        </Link>
        {view.opportunity_id ? (
          <>
            {" "}
            · opportunity <code>{view.opportunity_id}</code>
          </>
        ) : null}
      </p>
      {data.notices.length > 0 ? (
        <ul className="notices" role="status">
          {data.notices.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}
      {reviewing ? (
        <p className="notices" role="status">
          Read-only view of the world as of episode {view.episode}. Play next and Reset act on the live cursor —{" "}
          <Link href={replayHref(manifestId, null)}>go to latest</Link>.
        </p>
      ) : (
        <ReplayControls
          manifestId={manifestId}
          canPlayNext={view.can_play_next}
          canReset={view.episode > 0}
          advance={advanceEpisode}
          reset={resetReplayCursor}
        />
      )}
      <div className="replay-grid">
        <section aria-labelledby="rail-h" className="panel rail-panel">
          <h2 id="rail-h">Episodes</h2>
          <EpisodeRail view={view} manifestId={manifestId} />
        </section>
        <section aria-labelledby="detail-h" className="panel detail-panel">
          <h2 id="detail-h">Episode detail</h2>
          <EpisodeDetail view={view} at={data.at} />
        </section>
      </div>
    </section>
  );
}
