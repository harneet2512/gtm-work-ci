"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import type { AdvanceOutcome, ResetOutcome } from "@/app/replay/[manifestId]/actions";

interface Props {
  manifestId: string;
  /** Whether the replay has an unreleased event left (view.can_play_next at the live cursor). */
  canPlayNext: boolean;
  /** Whether anything is released to step back from (view.episode > 0 at the live cursor). */
  canReset: boolean;
  /** Server action: POST /replay/manifests/{id}/episodes/next. */
  advance: (manifestId: string) => Promise<AdvanceOutcome>;
  /** Server action: POST /replay/manifests/{id}/reset with { episode }. */
  reset: (manifestId: string, episode: number) => Promise<ResetOutcome>;
}

function advanceSummary(r: Extract<AdvanceOutcome, { ok: true }>): string {
  const verdict = r.material ? "material" : `No action required${r.noActionReason ? ` (${r.noActionReason})` : ""}`;
  const version = r.stateVersion === null ? "no state" : `state v${r.stateVersion}`;
  return `Released episode ${r.episode} of ${r.total}: ${verdict} · ${version}${r.coalesced ? " · coalesced fold" : ""}.`;
}

/**
 * The replay's mutation controls at the live cursor. Play next releases exactly one event through the
 * real pipeline; Reset returns the cursor to an earlier released position (bookkeeping is kept). The
 * outcome line reports the core's answer verbatim — never a fabricated success.
 */
export function ReplayControls({ manifestId, canPlayNext, canReset, advance, reset }: Props) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [line, setLine] = useState<{ text: string; error: boolean } | null>(null);

  const playNext = () => {
    startTransition(async () => {
      const r = await advance(manifestId);
      if (r.ok) {
        setLine({ text: advanceSummary(r), error: false });
        router.refresh();
      } else {
        setLine({ text: `Play next was refused by the core (${r.code}).`, error: true });
      }
    });
  };

  const resetToStart = () => {
    if (!window.confirm("Reset the replay cursor to episode 0? The released bookkeeping is kept; re-playing is deterministic.")) return;
    startTransition(async () => {
      const r = await reset(manifestId, 0);
      if (r.ok) {
        setLine({ text: `Replay cursor reset to episode ${r.episode}.`, error: false });
        router.refresh();
      } else {
        setLine({ text: `Reset was refused by the core (${r.code}).`, error: true });
      }
    });
  };

  return (
    <div className="controls" data-testid="replay-controls">
      <button type="button" onClick={playNext} disabled={!canPlayNext || pending}>
        Play next
      </button>
      <button type="button" className="secondary" onClick={resetToStart} disabled={!canReset || pending}>
        Reset
      </button>
      {pending ? <span className="hint">Asking the core…</span> : null}
      {line ? (
        <span className={`status-line${line.error ? " err" : ""}`} role="status">
          {line.text}
        </span>
      ) : null}
    </div>
  );
}
