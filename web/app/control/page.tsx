import { firstParam } from "@/lib/params";
import { getControlPage } from "@/lib/cached-loaders";
import { HealthBands, MaterialChanges, TrajectoryRail } from "@/components/control/ControlSections";
import { PipelineStrip } from "@/components/control/PipelineStrip";
import { ControlManifestForm } from "@/components/control/ControlManifestForm";
import { episodeProgress, playEvent } from "./actions";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * /control — the operator surface (HAR-145): the account world at N-1, the held-out event, Play, and the
 * four health bands over real replay/run/surface data. With no manifest the page asks for one — the core
 * has no manifest list endpoint.
 */
export default async function ControlPage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const manifestId = firstParam(sp.manifest)?.trim().toLowerCase() ?? null;

  if (!manifestId) {
    return (
      <section className="page">
        <h1>Control</h1>
        <p>
          The operator view of a frozen demo account: the world at the replay cursor, the held-out next event,
          Play event N through the real pipeline, and the health of Intelligence, Decision &amp; Learning,
          Cliff and System on what the backend actually did.
        </p>
        <ControlManifestForm initial="" />
      </section>
    );
  }

  const { replay, control } = await getControlPage(manifestId, firstParam(sp.at));
  const v = control;

  return (
    <section className="page control">
      <header className="control-head">
        <h1>{v.accountName ?? "Account"}</h1>
        <dl className="context-bar">
          <div>
            <dt>Episode</dt>
            <dd>
              {v.episode} / {v.total}
            </dd>
          </div>
          <div>
            <dt>Window</dt>
            <dd>{v.window}</dd>
          </div>
          <div>
            <dt>State</dt>
            <dd>{v.stateVersion != null ? `v${v.stateVersion}` : "none yet"}</dd>
          </div>
          <div>
            <dt>Run</dt>
            <dd>{v.run ? `${v.run.id.slice(0, 8)}…` : "none yet"}</dd>
          </div>
          <div>
            <dt>Opportunity</dt>
            <dd>{v.opportunityId ?? "—"}</dd>
          </div>
        </dl>
      </header>

      <section className="card replay-card" aria-label="Replay">
        <PipelineStrip
          manifestId={manifestId}
          accountId={v.accountId}
          canPlayNext={v.canPlayNext}
          nextEvent={v.nextEvent}
          play={playEvent}
          progress={episodeProgress}
        />
      </section>

      <HealthBands bands={v.bands} />

      <div className="control-grid">
        <MaterialChanges changes={v.changes} manifestId={v.manifestId} />
        <section aria-label="Episode trajectory" className="card">
          <h2>Episode trajectory</h2>
          <TrajectoryRail steps={v.trajectory} manifestId={manifestId} />
        </section>
      </div>

      {replay.notices.length > 0 ? (
        <ul className="notices" role="status">
          {replay.notices.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
