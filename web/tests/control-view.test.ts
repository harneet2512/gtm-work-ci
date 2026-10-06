// The /control view model (HAR-145): health bands, material changes and the trajectory rail, derived
// only from the replay view plus the account's run artifacts — nothing is asserted that isn't backed.
import { describe, expect, it } from "vitest";
import type { EpisodeReplayView, EvalRun, PipelineProgress } from "@/lib/api/types";
import { buildControlView, materialChanges, trajectory } from "@/lib/view/control";

type Ep = EpisodeReplayView["prior_episodes"][number];

const episode = (position: number, over: Partial<Ep> = {}): Ep => ({
  position,
  event_id: `0e7e0000-0000-4000-8000-00000000001${position}`,
  occurred_at: "2026-10-01T09:00:00Z",
  source_system: "email",
  provenance_origin: "dataset",
  provenance: "crmarena-pro:b2b",
  released: true,
  held_out: position > 1,
  material: true,
  account_change_id: `0a1c0000-0000-4000-8000-00000000030${position}`,
  decision_episode_id: `0de50000-0000-4000-8000-00000000020${position}`,
  state_version: 5 + position,
  graph_diff_id: 300 + position,
  no_action_reason: null,
  coalesced: false,
  ...over,
});

const view = (over: Partial<EpisodeReplayView> = {}): EpisodeReplayView => ({
  manifest_id: "0d3a0000-0000-4000-8000-000000000501",
  account_id: "0a0c0000-0000-4000-8000-000000000001",
  opportunity_id: "0c0f0000-0000-4000-8000-000000000004",
  episode: 2,
  total: 4,
  boundary: { historical_start: 1, historical_end: 3, live_start: 4, live_end: 4 },
  window: "historical",
  state: { version: 7, digest: "sha256:y", as_of: "2026-10-01T09:00:00Z", document: { account_name: "Acme Corp" } },
  knowledge: { as_of: "2026-10-01T09:00:00Z", items: [] },
  prior_episodes: [episode(1), episode(2, { material: false, decision_episode_id: null })],
  next_event: episode(3, { released: false, material: null, account_change_id: null, decision_episode_id: null, state_version: null, graph_diff_id: null }),
  can_previous: true,
  can_play_next: true,
  computed_at: "2026-10-04T06:00:00Z",
  ...over,
}) as EpisodeReplayView;

const base = (over: Partial<Parameters<typeof buildControlView>[0]> = {}) => ({
  view: view(),
  accountName: "Acme Corp",
  run: null,
  evalRun: null,
  episode: null,
  cliffKinds: [] as string[] | null,
  backendUnavailable: false,
  progress: null,
  ...over,
});

describe("buildControlView", () => {
  it("carries the replay's identity, cursor and withheld next event", () => {
    const v = buildControlView(base());
    expect(v).toMatchObject({ accountName: "Acme Corp", episode: 2, total: 4, canPlayNext: true, stateVersion: 7, knowledgeCount: 0 });
    expect(v.nextEvent).toEqual({ position: 3, occurredAt: "2026-10-01T09:00:00Z", source: "email", provenance: "crmarena-pro:b2b" });
    expect(v.decisionEpisodeId).toBe("0de50000-0000-4000-8000-000000000201");
  });

  it("builds four bands and puts the released world's facts under Intelligence", () => {
    const v = buildControlView(base());
    expect(v.bands.map((b) => b.id)).toEqual(["intelligence", "decision_learning", "cliff_experience", "system"]);
    const intelligence = v.bands[0]!;
    expect(intelligence.facts[0]).toContain("1 material event");
    expect(intelligence.facts.join(" ")).toContain("state v7");
    // No eval run was read, so no band claims health.
    expect(v.bands.every((b) => b.tone === "none")).toBe(true);
  });

  it("has no next event once everything is released, and null ids when no run or eval run exists", () => {
    const v = buildControlView(base({ view: view({ next_event: null, can_play_next: false }) }));
    expect(v.nextEvent).toBeNull();
    expect(v.canPlayNext).toBe(false);
    expect(v.evalRunId).toBeNull();
    expect(v.progress).toBeNull();
  });

  it("passes the strip's starting progress and the eval run id through", () => {
    const progress = { overall: "complete", stages: [] } as unknown as PipelineProgress;
    const v = buildControlView(base({ progress, evalRun: { id: "0e0a0000-0000-4000-8000-000000000001", areas: [] } as unknown as EvalRun }));
    expect(v.progress).toBe(progress);
    expect(v.evalRunId).toBe("0e0a0000-0000-4000-8000-000000000001");
  });
});

describe("trajectory", () => {
  it("ends with the held-out event as an unreleased step", () => {
    const steps = trajectory(view());
    expect(steps).toHaveLength(3);
    expect(steps[2]!.released).toBe(false);
    expect(steps[2]!.heldOut).toBe(true);
    expect(steps[2]!.position).toBe(3);
  });
});

describe("materialChanges", () => {
  it("lists only material episodes with their artifacts", () => {
    const changes = materialChanges(view());
    expect(changes).toHaveLength(1);
    expect(changes[0]!.position).toBe(1);
    expect(changes[0]!.detail).toContain("state v6");
    expect(changes[0]!.decisionEpisodeId).toBe("0de50000-0000-4000-8000-000000000201");
  });
});
