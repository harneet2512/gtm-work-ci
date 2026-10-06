import Link from "next/link";
import { firstParam } from "@/lib/params";
import { resolveManifest } from "@/lib/demo-manifest";
import { getControlPage } from "@/lib/cached-loaders";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * The /control inspector (HAR-145): the linked run at a glance — its steps, the eval tally across the
 * strategy bundles, and the door into the run's eval page. Everything read is the same data the bands
 * summarize; the inspector is where the detail lives.
 */
export default async function ControlInspector({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const { manifestId } = resolveManifest(firstParam(sp.manifest), process.env);
  if (!manifestId) return null;

  const { control, backendUnavailable } = await getControlPage(manifestId, firstParam(sp.at));
  const run = control.run;
  return (
    <div className="inspector-pane">
      <h2>Inspector</h2>
      {!run ? (
        <p className="hint">
          {backendUnavailable ? "Backend unavailable: the run list could not be read." : "No run yet — play a material event to open a decision episode."}
        </p>
      ) : (
        <>
          <dl className="kv">
            <div>
              <dt>Run</dt>
              <dd className="mono">{run.id.slice(0, 13)}…</dd>
            </div>
            <div>
              <dt>Phase</dt>
              <dd>{run.generation?.phase ?? "—"}</dd>
            </div>
            <div>
              <dt>Status</dt>
              <dd>{run.status ?? "—"}</dd>
            </div>
            <div>
              <dt>Episode</dt>
              <dd className="mono">{run.generation?.decision_episode_id?.slice(0, 13) ?? "—"}…</dd>
            </div>
          </dl>
          {(run.steps ?? []).length > 0 ? (
            <ol className="insp-steps">
              {(run.steps ?? []).map((s) => (
                <li key={s.seq} className={`step-${s.status}`}>
                  <span className="seq">{s.seq}</span>
                  <span className="step">{s.step}</span>
                  <span className="st">{s.status}</span>
                </li>
              ))}
            </ol>
          ) : null}
          <p>
            <Link href={`/runs/${run.id}/evals`}>Open this run's evals →</Link>
          </p>
        </>
      )}
    </div>
  );
}
