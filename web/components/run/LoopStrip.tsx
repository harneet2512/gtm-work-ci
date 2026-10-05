import type { LoopPhase } from "@/lib/view/run-loop";
import { NODE_MARK } from "@/components/episode/EpisodeBody";
import { NODE_WORD } from "@/lib/view/episode";

/**
 * The decision loop strip (HAR-145) above a run's detail: retrieve → reason → rank → act → learn,
 * each with its real status and one line of what happened. Status carries a word, never color alone.
 */
export function LoopStrip({ phases }: { phases: LoopPhase[] }) {
  return (
    <ol className="loop-strip" aria-label="Decision loop">
      {phases.map((p, i) => (
        <li key={p.id} className={`lphase st-${p.status}`}>
          <span className="lstep mono" aria-hidden="true">
            {i + 1}
          </span>
          <span className="mark" aria-hidden="true">
            {NODE_MARK[p.status]}
          </span>
          <span className="sr-only">{NODE_WORD[p.status]}</span>
          <span className="llabel">{p.label}</span>
          <span className="lsummary">{p.summary}</span>
          {p.detail ? <span className="ldetail mono">{p.detail}</span> : null}
        </li>
      ))}
    </ol>
  );
}
