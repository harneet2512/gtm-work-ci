"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import type { PipelineProgress } from "@/lib/api/types";
import type { PlayOutcome } from "@/app/control/actions";
import { fetchReplayProgress, type ProgressRead } from "@/lib/progress-client";
import { isFinalOverall, overallLine, PROBLEM_LABEL, stagesFromProgress, unavailableStages, waitingStages, type PipelineStage } from "@/lib/view/pipeline";
import { playRefusalLine, type PlayLine } from "@/lib/view/play-outcome";

interface Props {
  manifestId: string;
  /** Whether a held-out event is left to release. */
  canPlayNext: boolean;
  /** The withheld event's public metadata: its content stays withheld by contract. */
  nextEvent: { position: number; occurredAt: string; source: string; provenance: string | null } | null;
  /** The progress the page read when it rendered, so a reload shows what already ran. Null when none could be read. */
  initial?: PipelineProgress | null;
  play: (manifestId: string) => Promise<PlayOutcome>;
  fetchProgress?: (manifestId: string) => Promise<ProgressRead>;
  /** Poll period in ms (the product polls about every 1.5 s). */
  pollMs?: number;
  /** Polls allowed after Play returned before the strip stops refreshing and says so. */
  maxPolls?: number;
}

const DEFAULT_POLL_MS = 1500;
const DEFAULT_MAX_POLLS = 60;

/** The small line under a stage: why it did not complete, how long it took, and the core's own detail. */
const stageNote = (s: PipelineStage): string => [s.failureKind ? `failure: ${s.failureKind}` : null, s.duration, s.detail].filter(Boolean).join(" · ");

const sleep = (ms: number) => new Promise<void>((resolve) => window.setTimeout(resolve, ms));

/**
 * The Play-Event strip (HAR-145): the seven stages the real pipeline reports, never a timer. Play posts the release;
 * while that request is open and until the pipeline is complete or failed, the strip polls the progress read. A
 * finished stage reads "completed"; "passed" and the check mark belong to the eval verdict of the evals stage alone.
 * A poll that cannot reach the backend reads "backend unavailable": neither a failure nor "still running".
 */
export function PipelineStrip({ manifestId, canPlayNext, nextEvent, initial = null, play, fetchProgress = fetchReplayProgress, pollMs = DEFAULT_POLL_MS, maxPolls = DEFAULT_MAX_POLLS }: Props) {
  const router = useRouter();
  const [stages, setStages] = useState<PipelineStage[]>(() => (initial ? stagesFromProgress(initial) : waitingStages()));
  const [overall, setOverall] = useState<string | null>(() => (initial ? overallLine(initial) : null));
  const [line, setLine] = useState<PlayLine | null>(null);
  const [playing, setPlaying] = useState(false);
  const token = useRef(0);

  useEffect(() => {
    return () => {
      token.current += 1; // unmounting cancels any poll in flight
    };
  }, []);

  const show = (r: ProgressRead) => {
    if (r.ok) {
      setStages(stagesFromProgress(r.progress));
      setOverall(overallLine(r.progress));
    } else {
      setStages(unavailableStages());
      setOverall("Backend unavailable: progress could not be read");
    }
  };

  /** Polls until the pipeline is final AND the Play request has returned, so a stale final state cannot end it early. */
  const follow = async (mine: number, settled: { current: boolean }) => {
    let after = 0;
    while (token.current === mine) {
      const r = await fetchProgress(manifestId);
      if (token.current !== mine) return;
      show(r);
      if (settled.current) {
        if (r.ok && isFinalOverall(r.progress.overall)) {
          router.refresh();
          return;
        }
        after += 1;
        if (after >= maxPolls) {
          setLine({ text: "Progress stopped refreshing before the pipeline finished. Reload to read it again.", error: false });
          return;
        }
      }
      await sleep(pollMs);
    }
  };

  const onPlay = async () => {
    token.current += 1;
    const mine = token.current;
    const settled = { current: false };
    setPlaying(true);
    setLine(null);
    setStages(waitingStages());
    setOverall("Starting");
    const loop = follow(mine, settled);
    let outcome: PlayOutcome;
    try {
      outcome = await play(manifestId);
    } catch {
      // The action itself never answered (network, server restart): a transport problem, never an eval verdict.
      outcome = { ok: false, code: "unreachable" };
    }
    settled.current = true;
    if (!outcome.ok) {
      token.current += 1; // stop the loop; one last read shows what really ran
      setLine(playRefusalLine(outcome.code));
      show(await fetchProgress(manifestId));
      setPlaying(false);
      return;
    }
    const r = outcome.result;
    setLine({
      text: r.material
        ? `Event ${r.episode} of ${r.total} was material. The strip follows the real pipeline.`
        : `Event ${r.episode} of ${r.total}: no action required${r.no_action_reason ? ` (${r.no_action_reason})` : ""}.`,
      error: false,
    });
    await loop;
    setPlaying(false);
  };

  return (
    <div className="pipeline">
      <div className="pipeline-head">
        <h2>Play event {nextEvent?.position ?? "N"}</h2>
        {nextEvent ? (
          <p className="hint">
            Held out: {nextEvent.source} · {nextEvent.occurredAt}
            {nextEvent.provenance ? ` · ${nextEvent.provenance}` : ""} — its content is withheld until release.
          </p>
        ) : (
          <p className="hint">Every event in the manifest is released.</p>
        )}
        <button type="button" className="play" onClick={onPlay} disabled={playing || !canPlayNext}>
          {playing ? "Playing…" : "Play event N"}
        </button>
      </div>
      <ol className="pipeline-strip" aria-label="Pipeline stages" aria-live="polite">
        {stages.map((s, i) => (
          <li key={s.id} className={`stage st-${s.status}`}>
            <span className="stage-mark" aria-hidden="true">
              {s.mark}
            </span>
            <span className="stage-label">{s.label}</span>
            <span className="stage-status">
              {s.word}
              {s.problem ? <span className="stage-problem">{PROBLEM_LABEL[s.problem]}</span> : null}
            </span>
            {stageNote(s) ? <span className="stage-detail">{stageNote(s)}</span> : null}
            {i < stages.length - 1 ? <span className="stage-arrow" aria-hidden="true" /> : null}
          </li>
        ))}
      </ol>
      {overall ? (
        <p className="pipeline-overall" role="status">
          {overall}
        </p>
      ) : null}
      {line ? (
        <p className={`pipeline-line${line.error ? " error" : ""}`} role="status">
          {line.text}
        </p>
      ) : null}
    </div>
  );
}
