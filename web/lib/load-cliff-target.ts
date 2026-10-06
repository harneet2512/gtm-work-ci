// Where the rail's "Cliff messages" goes (HAR-145): the latest played episode, in Cliff mode. It is a redirect target, not
// a view of its own. No episode yet is an honest empty state; an unreadable backend is said so, never "no messages".
import type { CoreClient } from "@/lib/api/core-client";
import { CoreError } from "@/lib/api/core-client";
import { episodeHref } from "@/lib/view/episode";
import { withDemo } from "@/lib/view/demo-link";

export type CliffApi = Pick<CoreClient, "getReplayEpisodes">;

export const CLIFF_EMPTY_TEXT = "No Cliff messages yet — press Play on Control";
const CLIFF_UNAVAILABLE_TEXT = "Backend unavailable: Cliff messages could not be read";

export type CliffTarget = { kind: "redirect"; href: string } | { kind: "empty"; text: string } | { kind: "unavailable"; text: string };

export async function resolveCliffTarget(api: CliffApi, manifestId: string | null, demo: boolean): Promise<CliffTarget> {
  const empty: CliffTarget = { kind: "empty", text: CLIFF_EMPTY_TEXT };
  if (!manifestId) return empty;
  try {
    const view = await api.getReplayEpisodes(manifestId);
    const latest = [...view.prior_episodes].reverse().find((e) => e.decision_episode_id);
    if (!latest?.decision_episode_id) return empty;
    return { kind: "redirect", href: withDemo(episodeHref(latest.decision_episode_id, { mode: "cliff", manifest: manifestId }), demo) };
  } catch (e) {
    if (e instanceof CoreError && e.status === 404) return empty;
    return { kind: "unavailable", text: CLIFF_UNAVAILABLE_TEXT };
  }
}
