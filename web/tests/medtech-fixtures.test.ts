// The MedTech Advances demo fixtures (the real CRMArena-Pro case, rank 1 of the demo-case report) are what the
// screenshots and the e2e show, so they must be contract-valid, current with their builder, and keep world
// time: nothing after Event 13 is visible before it, and the reads follow ADR-0019 (world_as_of = N + 1µs).
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import type { AccountState, Graph, GraphDiff, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { FIXTURES_DIR, loadFixture, validateComponent, validateSchema } from "./contract-validator";
// @ts-expect-error - plain ESM builder without type declarations
import { MEDTECH_FILES, serialize } from "./fixtures/medtech/build.mjs";

const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");
const graph = loadFixture<Graph>("medtech.graph.json");
const before = loadFixture<Graph>("medtech.graph-before.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const sources = loadFixture<{ emails: { salesforce_id: string; occurred_at: string; position: number }[] }>("medtech.source-emails.json");
const EVENT_AT = "2023-11-09T09:30:00Z";

describe("MedTech fixtures are current with their builder", () => {
  it.each(Object.keys(MEDTECH_FILES as Record<string, unknown>))("%s", (file) => {
    const make = (MEDTECH_FILES as Record<string, () => unknown>)[file]!;
    expect(readFileSync(path.join(FIXTURES_DIR, file), "utf8").replaceAll("\r\n", "\n")).toBe(serialize(make()));
  });
});

describe("MedTech fixtures conform to the contract", () => {
  it.each([
    ["AccountSummary", "medtech.account.json"],
    ["Graph", "medtech.graph.json"],
    ["Graph", "medtech.graph-before.json"],
    ["GraphDiff", "medtech.graph-diff.json"],
    ["RunTrace", "medtech.run-trace.json"],
    ["RunStrategies", "medtech.run-strategies.json"],
  ])("%s: %s", (component, file) => {
    expect(validateComponent(component, loadFixture(file)).errors).toEqual([]);
  });

  it.each([
    ["account_state", "medtech.state.json"],
    ["account_state", "medtech.state-before.json"],
    ["agent_run", "medtech.agent-run.json"],
    ["human_strategy_decision", "medtech.strategy-decision.json"],
    ["judgment_inference", "medtech.judgment-inference.json"],
  ])("%s: %s", (schema, file) => {
    expect(validateSchema(schema, loadFixture(file)).errors).toEqual([]);
  });

  it("every timeline item is an activity", () => {
    for (const a of loadFixture<{ items: unknown[] }>("medtech.timeline.json").items) expect(validateSchema("activity", a).errors).toEqual([]);
  });
});

describe("MedTech ids resolve to the dataset and to each other", () => {
  const activities = [...trace.trigger_activities, ...(trace.correlated_activities ?? [])];

  it("is the real account and deal of the demo-case report", () => {
    expect(trace.state_at_run?.account_name).toBe("MedTech Advances");
    expect(activities.every((a) => a.opportunity_hint === "006Wt000007BHzBIAW")).toBe(true);
    expect(trace.trigger_activities[0]?.source_object_id).toBe("02sWt000001zsRGIAY");
  });

  it("every email activity is a real email of the deal, at its real time", () => {
    const real = new Map(sources.emails.map((e) => [e.salesforce_id, e.occurred_at]));
    for (const a of activities.filter((x) => x.source_system === "email")) expect(real.get(a.source_object_id)).toBe(a.occurred_at);
  });

  it("the decision, the inference and the bundles point at the run's own candidates", () => {
    const ids = strategies.strategy_set.candidates.map((c) => c.candidate_id);
    expect(ids).toContain(decision.selected_candidate_id);
    expect(ids).toContain(decision.original_agent_preference);
    expect(inference.human_choice).toBe(decision.selected_candidate_id);
    expect(inference.agent_preference).toBe(strategies.strategy_set.candidates.find((c) => c.preferred_by_agent)?.candidate_id);
    expect(strategies.eval_bundles.map((b) => b.strategy_candidate_id).sort()).toEqual([...ids].sort());
    expect(inference.human_strategy_decision_id).toBe(decision.id);
  });

  it("every graph edge joins two nodes of the same graph", () => {
    for (const g of [graph, before]) {
      const ids = new Set(g.nodes.map((n) => n.id));
      for (const e of g.edges) expect(ids.has(e.source) && ids.has(e.target), e.id).toBe(true);
    }
  });

  it("the graph diff adds exactly what the graph has and the N-1 graph lacks", () => {
    const now = new Set([...graph.nodes.map((n) => n.id), ...graph.edges.map((e) => e.id)]);
    const was = new Set([...before.nodes.map((n) => n.id), ...before.edges.map((e) => e.id)]);
    const added = diff.changes.filter((c) => c.op === "added").map((c) => c.id).sort();
    expect(added).toEqual([...now].filter((id) => !was.has(id)).sort());
    expect(diff.summary.added).toBe(added.length);
  });

  it("the graph holds every activity and every claim that holds, as the core's neighborhood does", () => {
    const timeline = loadFixture<{ items: { id: string; occurred_at: string }[] }>("medtech.timeline.json").items;
    const activityIds = (g: Graph) => g.nodes.filter((n) => n.type === "Activity" || n.type === "Conversation").map((n) => n.id).sort();
    expect(activityIds(graph)).toEqual(timeline.map((a) => a.id).sort());
    expect(activityIds(before)).toEqual(timeline.filter((a) => a.occurred_at < EVENT_AT).map((a) => a.id).sort());
    const stateClaims = new Set(Object.values(loadFixture<AccountState>("medtech.state.json").fields).flatMap((f) => f.evidence_refs.map((r) => r.claim_id).filter(Boolean)));
    for (const id of stateClaims) expect(graph.nodes.some((n) => n.id === id), String(id)).toBe(true);
  });

  it("Event 13 supersedes the old product use case: changed in the diff, gone from the After view", () => {
    const changed = diff.changes.filter((c) => c.op === "changed");
    expect(changed.map((c) => c.id)).toHaveLength(1);
    const [c] = changed;
    expect(before.nodes.some((n) => n.id === c!.id)).toBe(true);
    expect(graph.nodes.some((n) => n.id === c!.id)).toBe(false);
    expect(c!.changed).toEqual({ status: { before: "active", after: "superseded" } });
  });
});

describe("MedTech fixtures keep world time", () => {
  const after = loadFixture<AccountState>("medtech.state.json");
  const prior = loadFixture<AccountState>("medtech.state-before.json");

  it("the N-1 state is strictly before Event 13 and the run's state is at it", () => {
    expect(Date.parse(prior.as_of)).toBeLessThan(Date.parse(EVENT_AT));
    expect(after.as_of).toBe(EVENT_AT);
    expect(trace.state_before?.version).toBe(107);
    expect(trace.state_at_run?.version).toBe(108);
  });

  it("context pulls read the world at Event 13 + 1µs (ADR-0019)", () => {
    for (const c of trace.context_accesses) expect(c.world_as_of).toBe("2023-11-09T09:30:00.000001Z");
  });

  it("nothing in the N-1 world cites Event 13", () => {
    const heldOut = trace.trigger_activities[0]!.id;
    expect(JSON.stringify(prior)).not.toContain(heldOut);
    expect(JSON.stringify(before)).not.toContain(heldOut);
  });

  it("snapshot-only CRM values stay unknown as of Nov 9, 2023 (stage, amount)", () => {
    expect(after.fields.stage.known).toBe(false);
    expect(after.opportunities?.[0]?.stage).toBe("unknown");
    expect(after.opportunities?.[0]?.amount).toBeNull();
  });

  it("the diff changes exactly the report's fields for the held-out event", () => {
    expect(trace.state_diff?.changes.map((c) => c.field).sort()).toEqual(
      ["decision_criteria", "last_customer_interaction", "last_meaningful_change", "next_meeting", "next_milestone", "objections", "product_use_case"].sort(),
    );
  });

  it("the run, the judgments, the choice and the send happen in that order", () => {
    const judged = strategies.eval_bundles.flatMap((b) => b.items.flatMap((i) => (i.result ? [Date.parse(i.result.created_at)] : [])));
    expect(Math.max(...judged)).toBeLessThan(Date.parse(decision.chosen_at));
    expect(Date.parse(trace.run.created_at)).toBeGreaterThan(Date.parse(EVENT_AT));
    expect(Date.parse(decision.chosen_at)).toBeLessThan(Date.parse(decision.send_decided_at ?? ""));
    expect(Date.parse(inference.generated_at)).toBeGreaterThan(Date.parse(decision.send_decided_at ?? ""));
  });
});
