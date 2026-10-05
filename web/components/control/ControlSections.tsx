import Link from "next/link";
import { episodeHref } from "@/lib/view/episode";
import { TONE_MARK, TONE_WORD } from "@/lib/view/bands";
import type { ControlView, HealthBand, MaterialChange, TrajectoryStep } from "@/lib/view/control";

/** The four product-area bands: status the band rests on plus the one or two facts behind it. */
export function HealthBands({ bands }: { bands: HealthBand[] }) {
  return (
    <section aria-label="Area health" className="bands">
      {bands.map((b) => (
        <article key={b.id} className={`band tone-${b.tone}`}>
          <header>
            <span className="band-mark" aria-hidden="true">
              {TONE_MARK[b.tone]}
            </span>
            <h3>{b.label}</h3>
            <span className="sr-only">{TONE_WORD[b.tone]}</span>
          </header>
          <p className="band-status">{b.status}</p>
          {b.facts.length > 0 ? (
            <ul>
              {b.facts.map((f) => (
                <li key={f}>{f}</li>
              ))}
            </ul>
          ) : null}
        </article>
      ))}
    </section>
  );
}

/** What the released world changed: one line per material episode with its state/graph artifacts. */
export function MaterialChanges({ changes, manifestId = null }: { changes: MaterialChange[]; manifestId?: string | null }) {
  return (
    <section aria-label="Recent material changes" className="material-changes">
      <h2>Recent material changes</h2>
      {changes.length === 0 ? (
        <p className="hint">No released event has changed the world yet.</p>
      ) : (
        <ol>
          {changes.map((c) => (
            <li key={c.position}>
              <span className="pos">E{c.position}</span>
              <span className="what">{c.label}</span>
              {c.detail ? <span className="detail">{c.detail}</span> : null}
              {c.decisionEpisodeId ? (
                <Link href={episodeHref(c.decisionEpisodeId, { manifest: manifestId })} className="ep-link">
                  episode
                </Link>
              ) : null}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

const STEP_WORD = (s: TrajectoryStep): string =>
  !s.released ? "held out" : s.material === true ? "material" : s.material === false ? "no action" : "released";

/**
 * The episode trajectory: released episodes as settled steps, the held-out event outlined. Clicking a
 * released step reviews the world at that bound — the same `?at=` contract the replay page uses.
 */
export function TrajectoryRail({ steps, manifestId }: { steps: TrajectoryStep[]; manifestId: string }) {
  return (
    <nav aria-label="Episode trajectory" className="trajectory">
      <ol>
        {steps.map((s) => (
          <li key={s.position} className={`step ${s.released ? "released" : "held"}${s.material === true ? " material" : ""}`}>
            {s.released ? (
              <Link href={`/replay/${manifestId}?at=${s.position}`} aria-label={`Episode ${s.position}, ${STEP_WORD(s)}`}>
                <span className="pos">E{s.position}</span>
                <span className="what">{s.label}</span>
              </Link>
            ) : (
              <span aria-label={`Event ${s.position}, held out`}>
                <span className="pos">E{s.position}</span>
                <span className="what">{s.label}</span>
              </span>
            )}
            <span className="status">{STEP_WORD(s)}</span>
          </li>
        ))}
      </ol>
    </nav>
  );
}
