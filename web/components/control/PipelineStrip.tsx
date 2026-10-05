"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, useTransition } from "react";
import type { AdvanceResult } from "@/lib/api/types";
import type { EpisodeProgress, PlayOutcome } from "@/app/control/actions";
import { pipelineFromAdvance, waitingPipeline, type PipelineStage, type StageStatus } from "@/lib/view/pipeline";

interface Props {
  manifestId: string;
  accountId: string;
  /** Whether a held-out event is left to release. */
  canPlayNext: boolean;
  /** The withheld event's public metadata — content stays withheld by contract. */
  nextEvent: { position: number; occurredAt: string; source: string; provenance: string | null } | null;
  play: (manifestId: string) => Promise<PlayOutcome>;
  progress: (manifestId: string, accountId: string, decisionEpisodeId: string, accountChangeId: string | null) => Promise<EpisodeProgress>;
}

const MARK: Record<StageStatus, string> = {
  waiting: "○",
  running: "◐",
  passed: "✓",
  warning: "!",
  failed: "✗",
  skipped: "—",
  unknown: "?",
  unavailable: "⊘",
};

const STAGE_WORD: Record<StageStatus, string> = {
  waiting: "waiting",
  running: "running",
  passed: "passed",
  warning: "warning",
  failed: "failed",
  skipped: "skipped",
  unknown: "not observed",
  unavailable: "backend unavailable",
};

/** Keep polling while the run is mid-flight, or while a finished run could still surface a message. */
const settled = (stages: PipelineStage[]): boolean => {
  const decide = stages.find((s) => s.id === "decide");
  const cliff = stages.find((s) => s.id === "cliff");
  if (decide?.status === "running" || decide?.status === "unavailable") return false; // an outage may pass: keep polling (bounded)
  if (decide?.status === "passed" && (cliff?.status === "waiting" || cliff?.status === "running")) return false;
  return true;
};

const POLL_MS = 1500;
const POLL_MAX = 30;

/**
 * The Play-Event-N strip (HAR-145): six stages that light only when the backend artifact exists. Play
 * posts the advance; ingest/resolve/graph/state read straight off its result; the decide and cliff
 * stages follow the linked run and the episode's posted Cliff messages via bounded polling. A poll that
 * finds nothing yet leaves the stage at "running"/"waiting" — never a fabricated pass — and a poll that cannot
 * reach the core reads "backend unavailable", which is neither a failure nor "still running".
 */
export function PipelineStrip({ manifestId, accountId, canPlayNext, nextEvent, play, progress }: Props) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [stages, setStages] = useState<PipelineStage[]>(waitingPipeline);
  const [line, setLine] = useState<{ text: string; error: boolean } | null>(null);
  const live = useRef<AdvanceResult | null>(null);
  const stop = useRef(false);

  useEffect(() => {
    stop.current = false;
    return () => {
      stop.current = true;
    };
  }, []);

  const follow = (res: AdvanceResult, episodeId: string) => {
    let attempts = 0;
    const tick = async () => {
      if (stop.current) return;
      attempts += 1;
      const p = await progress(manifestId, accountId, episodeId, res.account_change_id).catch(() => null);
      if (stop.current) return;
      const next = pipelineFromAdvance(res, p?.run ?? null, p && !p.unavailable ? p.cliffPosted : null, p === null || p.unavailable);
      setStages(next);
      if (!settled(next) && attempts < POLL_MAX) window.setTimeout(tick, POLL_MS);
      else router.refresh();
    };
    window.setTimeout(tick, POLL_MS);
  };

  const onPlay = () => {
    startTransition(async () => {
      setLine(null);
      setStages(waitingPipeline());
      const r = await play(manifestId);
      if (!r.ok) {
        // A transport failure is not the core refusing Play: it reads "backend unavailable" in a neutral line.
        setLine(
          r.code === "unreachable"
            ? { text: "Backend unavailable: Play was not delivered, so nothing was released.", error: false }
            : { text: `Play was refused by the core (${r.code}).`, error: true },
        );
        return;
      }
      live.current = r.result;
      const first = pipelineFromAdvance(r.result, null, []);
      setStages(first);
      setLine({
        text: r.result.material
          ? `Event ${r.result.episode} of ${r.result.total} was material — the pipeline below follows the real run.`
          : `Event ${r.result.episode} of ${r.result.total}: no action required${r.result.no_action_reason ? ` (${r.result.no_action_reason})` : ""}.`,
        error: false,
      });
      if (r.result.decision_episode_id) follow(r.result, r.result.decision_episode_id);
      else router.refresh();
    });
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
        <button type="button" className="play" onClick={onPlay} disabled={pending || !canPlayNext}>
          {pending ? "Releasing…" : "Play event N"}
        </button>
      </div>
      <ol className="pipeline-strip" aria-label="Pipeline stages" aria-live="polite">
        {stages.map((s, i) => (
          <li key={s.id} className={`stage st-${s.status}`}>
            <span className="stage-mark" aria-hidden="true">
              {MARK[s.status]}
            </span>
            <span className="stage-label">{s.label}</span>
            <span className="stage-status">{STAGE_WORD[s.status]}</span>
            {s.detail ? <span className="stage-detail">{s.detail}</span> : null}
            {i < stages.length - 1 ? <span className="stage-arrow" aria-hidden="true" /> : null}
          </li>
        ))}
      </ol>
      {line ? (
        <p className={`pipeline-line${line.error ? " error" : ""}`} role="status">
          {line.text}
        </p>
      ) : null}
    </div>
  );
}
