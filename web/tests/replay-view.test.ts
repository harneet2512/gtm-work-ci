import { describe, expect, it } from "vitest";
import type { EpisodeEvent, EpisodeReplayView } from "@/lib/api/types";
import { badgeFor, boundaryEpisode, navFor, parseAt, railItems, replayHref, windowFor, windowLabel } from "@/lib/view/replay";
import { loadFixture } from "./contract-validator";

const view = loadFixture<EpisodeReplayView>("replay.episodes.json");
const [ep1, ep2] = view.prior_episodes;
const withheld = view.next_event!;

describe("badgeFor", () => {
  it("marks a released material episode as Material", () => {
    expect(badgeFor(ep1!)).toEqual({ kind: "material", label: "Material", reason: null });
  });

  it("marks a released non-material episode as No action required and keeps the reason", () => {
    const badge = badgeFor(ep2!);
    expect(badge.kind).toBe("no_action");
    expect(badge.label).toBe("No action required");
    expect(badge.reason).toBe("no_material_change");
  });

  it("marks an unreleased event as Withheld regardless of its held-out flag", () => {
    expect(badgeFor(withheld).kind).toBe("withheld");
    const heldOut: EpisodeEvent = { ...withheld, held_out: true, position: 4 };
    expect(badgeFor(heldOut).kind).toBe("withheld");
  });
});

describe("windowFor", () => {
  const b = view.boundary; // historical 1..3, live 4..4

  it("classifies positions into the historical and live windows", () => {
    expect(windowFor(b, 1)).toBe("historical");
    expect(windowFor(b, 3)).toBe("historical");
    expect(windowFor(b, 4)).toBe("live");
    expect(windowFor(b, 0)).toBe("none");
  });

  it("has no historical window when historical_end is 0", () => {
    expect(windowFor({ historical_start: 0, historical_end: 0, live_start: 1, live_end: 1 }, 1)).toBe("live");
    expect(windowLabel("none")).toBe("pre-release");
  });
});

describe("railItems", () => {
  it("orders boundary, released, withheld and future slots without naming the future", () => {
    const items = railItems(view); // k=2 of N=4
    expect(items.map((i) => i.state)).toEqual(["boundary", "released", "released", "withheld", "future"]);
    expect(items.map((i) => i.position)).toEqual([0, 1, 2, 3, 4]);
    expect(items[0]!.event).toBeNull();
    expect(items[3]!.event).toEqual(withheld);
    // Position 4 — the held-out event — is not even named at this boundary.
    expect(items[4]!.event).toBeNull();
    expect(items[2]!.selected).toBe(true);
    expect(items[0]!.selected).toBe(false);
  });

  it("selects the boundary at k=0 and withholds the first event", () => {
    const atZero: EpisodeReplayView = {
      ...view,
      episode: 0,
      window: "none",
      state: null,
      prior_episodes: [],
      next_event: { ...withheld, position: 1 },
      can_previous: false,
    };
    const items = railItems(atZero);
    expect(items[0]!.selected).toBe(true);
    expect(items[1]!.state).toBe("withheld");
    expect(items[1]!.event?.position).toBe(1);
    expect(items.slice(2).every((i) => i.state === "future")).toBe(true);
  });

  it("has no withheld slot when the replay is complete", () => {
    const done: EpisodeReplayView = {
      ...view,
      episode: 4,
      window: "live",
      prior_episodes: [...view.prior_episodes, { ...withheld, position: 3, released: true, material: true }, { ...withheld, position: 4, held_out: true, released: true, material: true }],
      next_event: null,
      can_play_next: false,
    };
    const items = railItems(done);
    expect(items).toHaveLength(5);
    expect(items.every((i) => i.state === "boundary" || i.state === "released")).toBe(true);
    expect(items[4]!.selected).toBe(true);
  });

  it("finds the boundary episode and reports none at k=0", () => {
    expect(boundaryEpisode(view)?.position).toBe(2);
    expect(boundaryEpisode({ ...view, episode: 0, prior_episodes: [] })).toBeNull();
  });
});

describe("navFor", () => {
  it("at the cursor offers only Previous into the released positions", () => {
    expect(navFor(view, null, 2)).toEqual({ prev: 1, next: null, latest: false });
  });

  it("at position 1 offers Previous to the boundary but no forward", () => {
    expect(navFor({ ...view, episode: 1 }, null, 1)).toEqual({ prev: 0, next: null, latest: false });
    expect(navFor({ ...view, episode: 0 }, null, 0)).toEqual({ prev: null, next: null, latest: false });
  });

  it("while reviewing offers Previous, Forward and Latest", () => {
    expect(navFor(view, 2, 4)).toEqual({ prev: 1, next: 3, latest: true });
    expect(navFor({ ...view, episode: 4 }, 4, 5)).toEqual({ prev: 3, next: 5, latest: true });
  });

  it("never offers Forward past the released cursor, and the live view reached via ?at has no Forward", () => {
    expect(navFor(view, 2, 3)).toEqual({ prev: 1, next: 3, latest: true });
    expect(navFor({ ...view, episode: 4 }, 4, 4)).toEqual({ prev: 3, next: null, latest: true });
    expect(navFor(view, 2, 2)).toEqual({ prev: 1, next: null, latest: true });
  });
});

describe("parseAt", () => {
  it("parses a bare position", () => {
    expect(parseAt(undefined)).toEqual({ at: null, malformed: false });
    expect(parseAt("")).toEqual({ at: null, malformed: false });
    expect(parseAt("0")).toEqual({ at: 0, malformed: false });
    expect(parseAt("12")).toEqual({ at: 12, malformed: false });
  });

  it("rejects non-integers and negatives as malformed", () => {
    for (const raw of ["x", "1.5", "-1", "1e3", "99999999999999999999"]) {
      expect(parseAt(raw), raw).toEqual({ at: null, malformed: true });
    }
  });
});

describe("replayHref", () => {
  it("drops the at parameter at the live cursor", () => {
    expect(replayHref("m", null)).toBe("/replay/m");
    expect(replayHref("m", 0)).toBe("/replay/m?at=0");
    expect(replayHref("m", 3)).toBe("/replay/m?at=3");
  });
});
