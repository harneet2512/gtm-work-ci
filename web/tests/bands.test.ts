// The four health bands (HAR-145): counts come from the account's latest EvalRun, one band per area. A band that has
// no results says "not measured"; a tone of ok/warn/fail comes only from real verdict counts; deltas are labelled
// "previous episode"; a failed read is "backend unavailable", never an eval failure.
import { describe, expect, it } from "vitest";
import type { EpisodeSummary, EvalRun } from "@/lib/api/types";
import { buildBands, deltaLine, toneOf } from "@/lib/view/bands";

type Area = EvalRun["areas"][number];
const counts = (pass = 0, warn = 0, fail = 0, unknown = 0, blocking = 0) => ({ pass, warn, fail, unknown, blocking_fail: blocking, total: pass + warn + fail + unknown });
const delta = (pass = 0, warn = 0, fail = 0, unknown = 0) => ({ pass, warn, fail, unknown, blocking_fail: 0, total: pass + warn + fail + unknown });

const area = (id: Area["area"], label: string, order: number, over: Partial<Area> = {}): Area => ({ area: id, label, order, measured: true, counts: counts(3), delta: null, ...over });

const evalRun = (areas: Area[]): EvalRun =>
  ({
    id: "0e0a0000-0000-4000-8000-000000000001",
    account_id: "0a0c0000-0000-4000-8000-000000000001",
    account_name: "Acme",
    decision_episode_id: "0de50000-0000-4000-8000-000000000202",
    run_status: "executed",
    evaluated_at: "2026-10-05T10:00:00Z",
    result_count: 9,
    counts: counts(9),
    areas,
    previous_eval_run_id: null,
  }) as EvalRun;

const full = (): EvalRun =>
  evalRun([
    area("intelligence", "Intelligence", 1, { counts: counts(4, 1), delta: delta(1, 0, -1) }),
    area("decision_learning", "Decision & Learning", 2, { counts: counts(2, 0, 1, 0, 1) }),
    area("cliff_experience", "Cliff / Experience", 3, { measured: false, counts: counts() }),
    area("system", "System", 4, { counts: counts(1, 0, 0, 2) }),
  ]);

const ctx = { cliffKinds: [] as string[] | null, episode: null as EpisodeSummary | null, unavailable: false, intelligenceFacts: [] as string[] };
const find = (bands: ReturnType<typeof buildBands>, id: string) => bands.find((b) => b.id === id)!;

describe("toneOf", () => {
  it("takes the worst real verdict: fail, warn, then unknown (unsure), then pass", () => {
    expect(toneOf(counts(5, 1, 1))).toBe("fail");
    expect(toneOf(counts(5, 1))).toBe("warn");
    expect(toneOf(counts(5, 0, 0, 1))).toBe("unsure");
    expect(toneOf(counts(5))).toBe("ok");
    expect(toneOf(counts())).toBe("none");
  });
});

describe("deltaLine", () => {
  it("labels the comparison 'previous episode' and lists only the counts that moved", () => {
    expect(deltaLine(delta(1, 0, -1))).toBe("vs previous episode: +1 pass · -1 fail");
  });

  it("says nothing moved, or that there is no previous episode", () => {
    expect(deltaLine(delta())).toBe("vs previous episode: no change");
    expect(deltaLine(null)).toBe("no previous episode to compare");
  });
});

describe("buildBands", () => {
  it("returns the four areas in order with their real counts and tones", () => {
    const bands = buildBands(full(), ctx);
    expect(bands.map((b) => b.id)).toEqual(["intelligence", "decision_learning", "cliff_experience", "system"]);
    const intel = find(bands, "intelligence");
    expect(intel.tone).toBe("warn");
    expect(intel.facts).toContain("4 pass · 1 warn · 0 fail · 0 unknown");
    expect(intel.facts).toContain("vs previous episode: +1 pass · -1 fail");
    expect(find(bands, "decision_learning").tone).toBe("fail");
    expect(find(bands, "decision_learning").facts).toContain("1 blocking");
    expect(find(bands, "system").tone).toBe("unsure");
  });

  it("reads an area with measured:false as not measured, with no tone, never a pass", () => {
    const cliff = find(buildBands(full(), ctx), "cliff_experience");
    expect(cliff.status).toBe("not measured");
    expect(cliff.tone).toBe("none");
    expect(cliff.facts.join(" ")).not.toMatch(/pass/);
  });

  it("keeps Cliff's posted messages as recorded facts, not as an eval verdict", () => {
    const cliff = find(buildBands(full(), { ...ctx, cliffKinds: ["bi", "chooser"] }), "cliff_experience");
    expect(cliff.tone).toBe("recorded");
    expect(cliff.status).toBe("not measured");
    expect(cliff.facts).toEqual(["Message 1 posted", "Message 2 posted"]);
  });

  it("does not let posted messages override a measured Cliff verdict", () => {
    const run = evalRun([area("intelligence", "Intelligence", 1), area("decision_learning", "Decision & Learning", 2), area("cliff_experience", "Cliff / Experience", 3, { counts: counts(2, 1) }), area("system", "System", 4)]);
    expect(find(buildBands(run, { ...ctx, cliffKinds: ["bi"] }), "cliff_experience").tone).toBe("warn");
  });

  it("adds the replay's own facts to Intelligence and the episode's final status to Decision & Learning", () => {
    const episode = { final_status: "sent" } as EpisodeSummary;
    const bands = buildBands(full(), { ...ctx, episode, intelligenceFacts: ["account state v7"] });
    expect(find(bands, "intelligence").facts).toContain("account state v7");
    expect(find(bands, "decision_learning").facts).toContain("episode: sent");
  });

  it("has no eval run yet: every band says so and none claims health", () => {
    const bands = buildBands(null, ctx);
    expect(bands).toHaveLength(4);
    expect(bands.every((b) => b.status === "no eval run yet" && b.tone === "none")).toBe(true);
  });

  it("reads an unreachable backend as backend unavailable on every band", () => {
    const bands = buildBands(null, { ...ctx, unavailable: true });
    expect(bands.every((b) => b.status === "backend unavailable" && b.tone === "none")).toBe(true);
  });

  it("an area missing from the eval run is not measured", () => {
    const bands = buildBands(evalRun([area("intelligence", "Intelligence", 1)]), ctx);
    expect(find(bands, "system").status).toBe("not measured");
  });

  it("labels the Cliff band's unreadable surface refs as not observable", () => {
    const run = evalRun([area("intelligence", "Intelligence", 1), area("decision_learning", "Decision & Learning", 2), area("cliff_experience", "Cliff / Experience", 3, { measured: false }), area("system", "System", 4)]);
    const cliff = find(buildBands(run, { ...ctx, cliffKinds: null }), "cliff_experience");
    expect(cliff.facts).toContain("Slack posts not observable");
  });
});
