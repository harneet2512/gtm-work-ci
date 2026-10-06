import type { CoreClient } from "@/lib/api/core-client";
import { CoreError, errorCode } from "@/lib/api/core-client";
import type { EpisodeReplayView } from "@/lib/api/types";
import { parseAt } from "@/lib/view/replay";

export type ReplayApi = Pick<CoreClient, "getReplayEpisodes">;

export interface ReplayPageData {
  view: EpisodeReplayView;
  /** The released position the URL asked for; null when the live cursor is shown. */
  at: number | null;
  /** The live released cursor: `?at` equal to it is the live view, below it is a review of history. */
  released: number;
  /** Sections that could not be honoured: the page still renders what it has. */
  notices: string[];
}

/** Only the core's error code is shown: the raw message can carry internal detail. */
const reason = (e: unknown): string => errorCode(e, e instanceof Error ? e.message : String(e));

/**
 * Reads the episode replay view at `?at` (or at the released cursor). A malformed `at` is ignored with a
 * notice; an `at` that names an unreleased position (core 422 `invalid_episode`) falls back to the cursor
 * with a notice rather than an error page. Every other failure propagates — the view is the page, so a
 * missing manifest (404) or an unavailable store (503) is fatal. An `at` view is paired with the live
 * cursor read so the page can tell a review of history apart from the live view reached via `?at=latest`.
 */
export async function loadReplayPage(api: ReplayApi, manifestId: string, rawAt: string | undefined): Promise<ReplayPageData> {
  const notices: string[] = [];
  const { at, malformed } = parseAt(rawAt);
  if (malformed) notices.push("The episode position in the URL is not a number, so it was ignored.");
  if (at === null) {
    const view = await api.getReplayEpisodes(manifestId);
    return { view, at: null, released: view.episode, notices };
  }
  const live = api.getReplayEpisodes(manifestId);
  // `live` is always awaited on success or on the 422 fallback; when the `at` call fails otherwise its
  // own error propagates and the live read's possible rejection must not go unhandled.
  live.catch(() => undefined);
  try {
    const [view, liveView] = await Promise.all([api.getReplayEpisodes(manifestId, { at }), live]);
    return { view, at, released: liveView.episode, notices };
  } catch (e) {
    if (e instanceof CoreError && e.status === 422 && e.code === "invalid_episode") {
      notices.push(`Episode ${at} is not a released position (${reason(e)}); the live cursor is shown instead.`);
      const view = await live;
      return { view, at: null, released: view.episode, notices };
    }
    throw e;
  }
}
