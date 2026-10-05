// The /episodes/[id] causal trajectory model (HAR-145): node order is the fixed causal chain and every
// status/summary is derived from the trace's own fields. A node is "recorded" when its payload exists,
// never "passed": a check mark is reserved for real EvalResult verdicts (HAR-145 / HAR-129).
import { describe, expect, it } from "vitest";
import type { AgentRun, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { buildEpisodeView, episodeHref, parseMode } from "@/lib/view/episode";
import { loadFixture } from "./contract-validator";

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");

const view = (posted: string[] | null = ["chooser", "judgment"]) =>
  buildEpisodeView(run, trace, strategies, decision, inference, posted);
const byId = (id: string) => view().nodes.find((n) => n.id === id)!;

describe("buildEpisodeView", () => {
  it("emits the causal chain in order", () => {
    expect(view().nodes.map((n) => n.id)).toEqual([
      "source_event",
      "evidence",
      "resolution",
      "graph",
      "state",
      "precedents",
      "knowledge",
      "knowledge_applicable",
      "knowledge_cited",
      "knowledge_influence",
      "candidates",
      "ranking",
      "cliff",
      "human",
      "recompute",
      "knowledge_mutation",
    ]);
  });

  it("reads the medtech trigger and its correlated evidence", () => {
    expect(byId("source_event").status).toBe("recorded");
    expect(byId("source_event").summary).toContain("Fatoumata");
    expect(byId("evidence").status).toBe("recorded");
    expect(byId("evidence").summary).toContain("6 correlated activities");
    expect(byId("resolution").status).toBe("recorded");
  });

  it("reports the state move the diff carried", () => {
    expect(byId("graph").status).toBe("recorded");
    expect(byId("graph").summary).toContain("7 changes");
    expect(byId("state").summary).toContain("v107 → v108");
  });

  it("keeps retrieval, applicability, citation and influence as four separate nodes", () => {
    // MedTech's trace reads state+evidence only and cites zero knowledge items.
    expect(byId("knowledge").status).toBe("absent");
    expect(byId("knowledge_cited").status).toBe("absent");
    expect(byId("precedents").status).toBe("recorded");
    expect(byId("knowledge_applicable").status).toBe("not_observable");
    expect(byId("knowledge_influence").status).toBe("not_observable");
    expect(byId("knowledge_influence").summary).toContain("not measured");
  });

  it("never lets a citation read as retrieval or influence", () => {
    const refs = ["11110000-0000-4000-8000-000000000001"];
    const cited = {
      ...strategies,
      strategy_set: { ...strategies.strategy_set, candidates: strategies.strategy_set.candidates.map((c, i) => (i === 0 ? { ...c, knowledge_refs: refs } : c)) },
    } as RunStrategies;
    const v = buildEpisodeView(run, trace, cited, decision, inference, []);
    const get = (id: string) => v.nodes.find((n) => n.id === id)!;
    expect(get("knowledge_cited").status).toBe("recorded");
    expect(get("knowledge_cited").summary).toContain("1 knowledge item");
    expect(get("knowledge").status).toBe("absent"); // cited without a traced retrieval stays "not recorded"
    expect(get("knowledge_influence").status).toBe("not_observable");
    expect(get("knowledge_influence").summary).toContain("not measured");
    const read = { ...trace, context_accesses: [{ access_id: 1, tool: "knowledge", items: [1] }] } as unknown as RunTrace;
    const v2 = buildEpisodeView(run, read, strategies, decision, inference, []);
    expect(v2.nodes.find((n) => n.id === "knowledge")!.status).toBe("recorded");
    expect(v2.nodes.find((n) => n.id === "knowledge_cited")!.status).toBe("absent");
  });

  it("never reports any node as passed or failed (reserved for EvalResult verdicts)", () => {
    for (const n of view().nodes) expect(["recorded", "absent", "not_observable", "waiting"]).toContain(n.status);
  });

  it("carries the three candidates and Ghost's pick", () => {
    expect(byId("candidates").summary).toContain("3 candidates");
    expect(byId("ranking").status).toBe("recorded");
    expect(byId("ranking").summary).toContain("Book the call");
  });

  it("cliff reflects posted surface refs — waiting when none, unknown when unreadable", () => {
    expect(byId("cliff").status).toBe("recorded");
    expect(byId("cliff").summary).toContain("Message 2 + Message 3");
    expect(view([]).nodes.find((n) => n.id === "cliff")!.status).toBe("waiting");
    expect(view(null).nodes.find((n) => n.id === "cliff")!.status).toBe("not_observable");
  });

  it("reads the human edit from the trace decision and infers the override", () => {
    expect(byId("human").summary).toContain("edit");
    // The core exposes no invalidation record, so an edit alone never proves a recompute.
    expect(byId("recompute").status).toBe("not_observable");
    expect(byId("recompute").summary).toContain("not observable");
    expect(byId("recompute").summary).toContain("no invalidation record");
    expect(byId("knowledge_mutation").status).toBe("waiting");
    expect(byId("knowledge_mutation").summary).toContain("overrode");
  });

  it("falls back honestly when artifacts are absent", () => {
    const bare = { ...trace, decisions: [], correlated_activities: [], context_accesses: [] };
    const v = buildEpisodeView(run, bare, null, null, null, []);
    const get = (id: string) => v.nodes.find((n) => n.id === id)!;
    expect(get("evidence").status).toBe("absent");
    expect(get("precedents").status).toBe("absent");
    expect(get("candidates").status).toBe("absent");
    expect(get("ranking").status).toBe("absent");
    expect(get("human").status).toBe("waiting");
    expect(get("recompute").status).toBe("not_observable");
    expect(get("knowledge_mutation").status).toBe("absent");
  });
});

describe("parseMode", () => {
  it("defaults to story and accepts the four modes", () => {
    expect(parseMode(undefined)).toBe("story");
    expect(parseMode("nonsense")).toBe("story");
    for (const m of ["story", "trace", "graphdiff", "raw"] as const) expect(parseMode(m)).toBe(m);
  });
});

describe("episodeHref", () => {
  it("builds the bare path, and keeps node, mode and manifest in a stable order", () => {
    expect(episodeHref("e1")).toBe("/episodes/e1");
    expect(episodeHref("e1", { node: "graph", mode: "trace" })).toBe("/episodes/e1?node=graph&mode=trace");
    expect(episodeHref("e1", { mode: "cliff", manifest: "m1" })).toBe("/episodes/e1?mode=cliff&manifest=m1");
    expect(episodeHref("e1", { manifest: null, node: null })).toBe("/episodes/e1");
  });
});
