// The /control view model (HAR-145): health bands, material changes and the trajectory rail, derived
// only from the replay view plus the account's run artifacts — nothing is asserted that isn't backed.
import { describe, expect, it } from "vitest";
import type { AgentRun, EpisodeReplayView, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import { buildControlView, materialChanges, trajectory } from "@/lib/view/control";
import { loadExample, loadFixture } from "./contract-validator";

const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");

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

const run = (phase: string, failed = 0): AgentRun =>
  ({
    id: "0a120000-0000-4000-8000-0000000000e5",
    account_id: "0a0c0000-0000-4000-8000-000000000001",
    generation: { phase },
    steps: [
      { seq: 1, step: "retrieve", status: "succeeded" },
      ...(failed ? [{ seq: 2, step: "judge", status: "failed" }] : [{ seq: 2, step: "judge", status: "succeeded" }]),
    ],
  }) as AgentRun;

const band = (v: ReturnType<typeof buildControlView>, id: string) => v.bands.find((b) => b.id === id);

describe("buildControlView bands", () => {
  it("reports intelligence from the released world's own counts", () => {
    const v = buildControlView(view(), "Acme Corp", null, null, null, []);
    const b = band(v, "intelligence")!;
    expect(b.status).toBe("the world the agent saw");
    expect(b.facts[0]).toContain("1 material event");
    expect(b.facts.join(" ")).toContain("state v7");
    // No intelligence eval is read here, so the band never claims health.
    expect(b.tone).toBe("none");
    expect(b.facts).toContain("intelligence evals not observable here");
  });

  it("says no agent run yet when the account has none", () => {
    const v = buildControlView(view(), null, null, null, null, []);
    expect(band(v, "decision_learning")!.status).toBe("no agent run yet");
  });

  it("reads the run's phase and the eval tally for decision & learning", () => {
    const v = buildControlView(view(), null, run("published"), strategies, decision, ["chooser"]);
    const b = band(v, "decision_learning")!;
    expect(b.status).toBe("run published");
    expect(b.facts[0]).toContain("fail");
    expect(b.tone).toBe("fail"); // the Acme bundles carry fails
    expect(v.decisionEpisodeId).toBe("0de50000-0000-4000-8000-000000000201");
  });

  it("says 'backend unavailable' - not 'no agent run yet' - when the run list could not be read", () => {
    const v = buildControlView(view(), null, null, null, null, [], true);
    const b = band(v, "decision_learning")!;
    expect(b.status).toBe("backend unavailable");
    expect(b.tone).toBe("none");
    expect(band(v, "system")!.status).toBe("backend unavailable");
    expect(band(buildControlView(view(), null, null, null, null, [], false), "decision_learning")!.status).toBe("no agent run yet");
  });

  it("labels posted Cliff kinds and stays quiet when none posted", () => {
    const posted = buildControlView(view(), null, null, null, null, ["bi", "chooser"]);
    expect(band(posted, "cliff_experience")!.facts).toEqual(["Message 1", "Message 2"]);
    const none = buildControlView(view(), null, null, null, null, []);
    expect(band(none, "cliff_experience")!.status).toBe("nothing sent yet");
    const unreadable = buildControlView(view(), null, null, null, null, null);
    expect(band(unreadable, "cliff_experience")!.status).toBe("not observable");
  });

  it("never gives Cliff or System a check-mark tone: posted messages and done steps are recorded, not eval verdicts", () => {
    expect(band(buildControlView(view(), null, null, null, null, ["chooser"]), "cliff_experience")!.tone).toBe("recorded");
    expect(band(buildControlView(view(), null, run("published"), null, null, []), "system")!.tone).toBe("recorded");
  });

  it("marks the system band failing when a run step failed", () => {
    const v = buildControlView(view(), null, run("published", 1), null, null, []);
    const b = band(v, "system")!;
    expect(b.tone).toBe("fail");
    expect(b.facts.join(" ")).toContain("1 failed");
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
