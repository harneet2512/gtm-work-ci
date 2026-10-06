// The run loop strip (HAR-145): five phases derived from the trace/strategies/decision/inference —
// a phase is "recorded" when its artifact exists (never "passed"); an absent artifact yields absent/waiting.
import { describe, expect, it } from "vitest";
import type { AgentRun, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { buildLoop } from "@/lib/view/run-loop";
import { loadFixture } from "./contract-validator";

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");

const loop = (t = trace) => buildLoop(run, t, strategies, decision, inference);
const phase = (id: string, t = trace) => loop(t).find((p) => p.id === id)!;

describe("buildLoop", () => {
  it("emits the five phases in loop order", () => {
    expect(loop().map((p) => p.id)).toEqual(["retrieve", "reason", "rank", "act", "learn"]);
  });

  it("reads medtech's real loop", () => {
    expect(phase("retrieve").summary).toContain("Context read from 2 tools");
    expect(phase("retrieve").status).toBe("recorded");
    expect(phase("reason").summary).toContain("signal");
    expect(phase("rank").summary).toContain("Book the call");
    expect(phase("act").status).toBe("recorded"); // status recorded = executed
    expect(phase("act").summary).toContain("edit");
    expect(phase("learn").status).toBe("waiting"); // inference exists, mutation not confirmed
    expect(phase("learn").summary).toContain("overrode");
  });

  it("stays honest when the trace is empty", () => {
    const bare = { ...trace, context_accesses: [], signals: [], trigger_evaluation: undefined, decisions: [], placeholders: {} } as unknown as RunTrace;
    expect(phase("retrieve", bare).status).toBe("absent");
    expect(phase("reason", bare).status).toBe("absent");
    // inference still exists → learning is inferred-but-unconfirmed; none at all → skipped.
    expect(phase("learn", bare).status).toBe("waiting");
    expect(buildLoop(run, bare, strategies, decision, null).find((p) => p.id === "learn")!.status).toBe("absent");
  });

  it("keeps rank recorded when no candidate is preferred, absent without a set", () => {
    const noPick = { ...strategies, strategy_set: { ...strategies.strategy_set, candidates: strategies.strategy_set.candidates.map((c) => ({ ...c, preferred_by_agent: false })) } };
    expect(buildLoop(run, trace, noPick, decision, inference).find((p) => p.id === "rank")!.status).toBe("recorded");
    expect(buildLoop(run, trace, null, decision, inference).find((p) => p.id === "rank")!.status).toBe("absent");
  });
});
