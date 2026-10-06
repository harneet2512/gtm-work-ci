// "Cliff messages" in the rail lands on the latest played episode with Cliff mode selected. It is a redirect, not a page:
// no episode yet reads as an honest empty state, and an unreadable backend never reads as "no messages".
import { describe, expect, it } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import type { EpisodeReplayView } from "@/lib/api/types";
import { CLIFF_EMPTY_TEXT, resolveCliffTarget } from "@/lib/load-cliff-target";
import { loadFixture } from "./contract-validator";

const view = loadFixture<EpisodeReplayView>("replay.episodes.json");
const MANIFEST = view.manifest_id;
const E1 = "0de50000-0000-4000-8000-000000000201";
const E2 = "0de50000-0000-4000-8000-000000000202";
const withEpisodes = (ids: (string | null)[]): EpisodeReplayView => ({
  ...view,
  prior_episodes: ids.map((id, i) => ({ ...(view.prior_episodes[0] ?? ({} as never)), position: i + 1, decision_episode_id: id })) as never,
});
const api = (v: EpisodeReplayView | Error) => ({
  getReplayEpisodes: async () => {
    if (v instanceof Error) throw v;
    return v;
  },
});

describe("resolveCliffTarget", () => {
  it("redirects to the newest played episode in Cliff mode", async () => {
    const t = await resolveCliffTarget(api(withEpisodes([E1, E2])), MANIFEST, false);
    expect(t).toEqual({ kind: "redirect", href: `/episodes/${E2}?mode=cliff&manifest=${MANIFEST}` });
  });
  it("skips trailing episodes that opened no decision", async () => {
    const t = await resolveCliffTarget(api(withEpisodes([E1, null])), MANIFEST, false);
    expect(t).toMatchObject({ kind: "redirect", href: expect.stringContaining(E1) });
  });
  it("carries the demo flag", async () => {
    const t = await resolveCliffTarget(api(withEpisodes([E1])), MANIFEST, true);
    expect(t).toMatchObject({ href: expect.stringContaining("demo=1") });
  });
  it("shows the empty state before any episode was played", async () => {
    expect(await resolveCliffTarget(api(withEpisodes([null])), MANIFEST, false)).toEqual({ kind: "empty", text: CLIFF_EMPTY_TEXT });
    expect(await resolveCliffTarget(api(withEpisodes([])), MANIFEST, false)).toEqual({ kind: "empty", text: CLIFF_EMPTY_TEXT });
    expect(CLIFF_EMPTY_TEXT).toBe("No Cliff messages yet — press Play on Control");
  });
  it("shows the empty state when no demo account is configured", async () => {
    expect(await resolveCliffTarget(api(withEpisodes([E1])), null, false)).toEqual({ kind: "empty", text: CLIFF_EMPTY_TEXT });
  });
  it("says backend unavailable on a transport error, never 'no messages'", async () => {
    const t = await resolveCliffTarget(api(new CoreError(0, "unreachable", "x")), MANIFEST, false);
    expect(t).toEqual({ kind: "unavailable", text: "Backend unavailable: Cliff messages could not be read" });
  });
  it("treats an unknown manifest (404) as nothing played", async () => {
    expect(await resolveCliffTarget(api(new CoreError(404, "manifest_not_found", "x")), MANIFEST, false)).toMatchObject({ kind: "empty" });
  });
});
