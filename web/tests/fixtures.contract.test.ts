// The recorded core responses the web tests and the Playwright run render must satisfy the contract,
// so a contract change fails here before it can silently stale the fixtures.
import { describe, expect, it } from "vitest";
import { loadExample, loadFixture, validateComponent, validateSchema } from "./contract-validator";
import type { Graph, GraphDiff } from "@/lib/api/types";

describe("recorded core fixtures conform to the contract", () => {
  it("rejects a broken document (the validator is not vacuous)", () => {
    expect(validateComponent("Graph", { account_id: "x" }).valid).toBe(false);
    expect(validateComponent("GraphDiff", {}).valid).toBe(false);
  });

  it("GET /accounts/{id}/graph", () => {
    const result = validateComponent("Graph", loadFixture("acme.graph.json"));
    expect(result.errors).toEqual([]);
  });

  it("GET /accounts/{id}/graph?world_as_of=<occurred_at(N)> (the N-1 world graph)", () => {
    const before = loadFixture<Graph>("acme.graph-before.json");
    expect(validateComponent("Graph", before).errors).toEqual([]);
    // The world strictly before N has none of N's additions and has the edge N removed.
    const ids = new Set(before.nodes.map((n) => n.id));
    expect(ids.has("0ac70000-0000-4000-8000-000000000101")).toBe(false);
    expect(ids.has("0c1a0000-0000-4000-8000-000000000201")).toBe(false);
    expect(before.edges.some((e) => e.id === "0ed90000-0000-4000-8000-000000000099")).toBe(true);
    expect(before.projection.projected_at).toBeNull();
  });

  it("GET /events/{id}/graph-diff", () => {
    const result = validateComponent("GraphDiff", loadFixture("acme.graph-diff.json"));
    expect(result.errors).toEqual([]);
  });

  it("GET /accounts/{id}/timeline items", () => {
    const timeline = loadFixture<{ items: unknown[] }>("acme.timeline.json");
    expect(timeline.items.length).toBeGreaterThan(1);
    for (const item of timeline.items) expect(validateSchema("activity", item).errors).toEqual([]);
  });

  it("GET /accounts list", () => {
    const list = loadFixture<{ items: unknown[] }>("accounts.json");
    for (const item of list.items) expect(validateComponent("AccountSummary", item).errors).toEqual([]);
  });

  it("GET /accounts/{id}/state is the contract example", () => {
    expect(validateSchema("account_state", loadExample("account_state")).errors).toEqual([]);
  });

  it("GET /replay/manifests/{id}/episodes (the recorded replay view)", () => {
    const episodes = loadFixture<{
      episode: number;
      total: number;
      prior_episodes: { position: number; released: boolean; material: boolean | null }[];
      next_event: { position: number; released: boolean; material: unknown; state_version: unknown; graph_diff_id: unknown; held_out: boolean } | null;
    }>("replay.episodes.json");
    expect(validateSchema("episode_replay", episodes).errors).toEqual([]);
    // The view is at released position 2 of 4: two released episodes, the withheld third, the unnamed fourth.
    expect(episodes.episode).toBe(2);
    expect(episodes.total).toBe(4);
    expect(episodes.prior_episodes.map((e) => e.position)).toEqual([1, 2]);
    expect(episodes.prior_episodes.every((e) => e.released && e.material !== null)).toBe(true);
    // The withheld next event carries no consequences (HAR-129 B: no future leakage).
    expect(episodes.next_event).toMatchObject({ position: 3, released: false, held_out: false, material: null, state_version: null, graph_diff_id: null });
  });

  it("the e2e replay world is a contiguous sequence whose last event is held out", () => {
    const world = loadFixture<{ events: { position: number; held_out: boolean; release: object }[] }>("replay.events.json");
    expect(world.events.map((e) => e.position)).toEqual([1, 2, 3, 4]);
    expect(world.events.slice(0, -1).every((e) => !e.held_out)).toBe(true);
    expect(world.events[world.events.length - 1]!.held_out).toBe(true);
    // Every event carries the bookkeeping a release would report.
    for (const e of world.events) expect(Object.keys(e.release)).toEqual(
      expect.arrayContaining(["material", "account_change_id", "decision_episode_id", "state_version", "graph_diff_id", "no_action_reason", "coalesced"]),
    );
  });

  it("GET /accounts/{id}/state?world_as_of=<occurred_at(N)> (the state strictly before the Play event)", () => {
    const before = loadFixture<{
      open_transition: unknown;
      fields: Record<string, { known: boolean }>;
      opportunities: { health: string; as_of: string; last_activity_id: string }[];
    }>("acme.state-before.json");
    expect(validateSchema("account_state", before).errors).toEqual([]);
    expect(before.open_transition).toBeNull();
    expect(before.fields["health"]!.known).toBe(false);
    // The before state carries no value or evidence of event N.
    expect(before.opportunities[0]!.health).toBe("unknown");
    expect(before.opportunities[0]!.last_activity_id).toBe("0ac70000-0000-4000-8000-000000000100");
    expect(JSON.stringify(before)).not.toContain("2026-09-29T15:42:00Z");
  });

  it("GET /runs/{id}/trace is a real RunTrace: the run, its trigger + correlated activities, states, diff, signals, decisions", () => {
    const trace = loadFixture<{
      run: { id: string };
      trigger_activities: { id: string; occurred_at: string }[];
      state_before: { fields: Record<string, { known: boolean }> };
      state_at_run: { fields: Record<string, { known: boolean }> };
      decisions: { agent_run_id: string }[];
      placeholders: { eval_runs: unknown[]; customer_reactions: unknown[]; knowledge_updates: unknown[] };
    }>("acme.run-trace.json");
    expect(validateComponent("RunTrace", trace).errors).toEqual([]);
    // The trace is internally consistent: it is the trace OF run ...0601, triggered by the email N.
    expect(trace.run.id).toBe("0f0a0000-0000-4000-8000-000000000601");
    expect(trace.trigger_activities.map((a) => a.id)).toEqual(["0ac70000-0000-4000-8000-000000000101"]);
    for (const d of trace.decisions) expect(d.agent_run_id).toBe(trace.run.id);
    // State actually moved: health unknown before, known after (the N-1 -> N step the diff records).
    expect(trace.state_before.fields["health"]!.known).toBe(false);
    expect(trace.state_at_run.fields["health"]!.known).toBe(true);
    // One placeholder populated (the customer's positive reply), the rest honest WP21/WP22 slots.
    expect(trace.placeholders.customer_reactions).toHaveLength(1);
    expect(trace.placeholders.eval_runs).toEqual([]);
    expect(trace.placeholders.knowledge_updates).toEqual([]);
  });

  it("the recorded world is internally consistent: every reference resolves and before-values match", () => {
    const trace = loadFixture<{
      run: {
        id: string;
        status: string;
        run_mode: string;
        generation: { strategy_set_id: string; decision_episode_id: string };
        input_context_refs: { access_id: number }[];
        steps: { step: string; status: string; external_effect_id: string | null }[];
        output: { finished_artifact: unknown };
      };
      trigger_evaluation: { signal_ids: string[] };
      context_accesses: { access_id: number; tool: string }[];
      signals: { id: string }[];
      state_before: {
        fields: Record<string, { value?: unknown }>;
        buying_group: { person_id: string }[];
      };
      state_diff: { changes: { field: string; op: string; before: unknown }[] };
      decisions: { decision: string; edited_artifact?: unknown }[];
      placeholders: { customer_reactions: { activity_id: string }[] };
    }>("acme.run-trace.json");
    const strategies = loadFixture<{
      strategy_set: { id: string; decision_episode_id: string };
      eval_bundles: { items: { result: { id: string } | null }[] }[];
    }>("acme.run-strategies.json");
    const timeline = loadFixture<{ items: { id: string }[] }>("acme.timeline.json");
    const timelineIds = new Set(timeline.items.map((a) => a.id));

    // generation names the objects the world actually contains.
    expect(trace.run.generation.strategy_set_id).toBe(strategies.strategy_set.id);
    expect(trace.run.generation.decision_episode_id).toBe(strategies.strategy_set.decision_episode_id);
    // every cited signal is carried by the trace.
    const signalIds = new Set(trace.signals.map((s) => s.id));
    for (const id of trace.trigger_evaluation.signal_ids) expect(signalIds.has(id), id).toBe(true);
    // every context pull the run recorded is served as an access.
    const accessIds = new Set(trace.context_accesses.map((a) => a.access_id));
    for (const ref of trace.run.input_context_refs) expect(accessIds.has(ref.access_id), `access ${ref.access_id}`).toBe(true);
    // diff.before equals the before-state's actual value for each changed field, and an
    // "added" buying-group member was genuinely absent before the event.
    const v6group = new Set(trace.state_before.buying_group.map((p) => p.person_id));
    for (const change of trace.state_diff.changes) {
      if (change.field === "buying_group" && change.op === "added") {
        expect(change.before).toBeNull();
        const after = change as unknown as { after: { person_id: string } };
        expect(v6group.has(after.after.person_id), `v6 already contains ${after.after.person_id}`).toBe(false);
      } else {
        expect(JSON.stringify(change.before), change.field).toBe(JSON.stringify(trace.state_before.fields[change.field]?.value));
      }
    }
    // eval result ids are unique across all bundles.
    const resultIds = strategies.eval_bundles.flatMap((b) => b.items.map((i) => i.result?.id).filter(Boolean));
    expect(new Set(resultIds).size).toBe(resultIds.length);
    // a live run that was executed exposes the effect on its execute step, and the finished
    // artifact is the human-edited one, not the agent's original draft.
    if (trace.run.run_mode === "live" && trace.run.status === "executed") {
      const execute = trace.run.steps.find((s) => s.step === "execute");
      expect(execute?.external_effect_id).toBeTruthy();
      const edit = trace.decisions.find((d) => d.decision === "edit");
      if (edit) expect(trace.run.output.finished_artifact).toEqual(edit.edited_artifact);
    }
    // every reaction cites an activity that exists in the world.
    for (const r of trace.placeholders.customer_reactions) expect(timelineIds.has(r.activity_id), r.activity_id).toBe(true);
  });

  it("GET /runs/{id}/strategies is the strategy set plus exactly one bundle per candidate", () => {
    const strategies = loadFixture<{
      strategy_set: { candidates: { candidate_id: string; eval_bundle_ref: string; draft_index: number }[] };
      eval_bundles: { id: string; strategy_candidate_id: string; draft_index: number }[];
    }>("acme.run-strategies.json");
    expect(validateComponent("RunStrategies", strategies).errors).toEqual([]);
    expect(strategies.eval_bundles).toHaveLength(3);
    // Every candidate's eval_bundle_ref resolves to a bundle that points back at it (HAR-129 §9).
    for (const c of strategies.strategy_set.candidates) {
      const bundle = strategies.eval_bundles.find((b) => b.id === c.eval_bundle_ref);
      expect(bundle, c.candidate_id).toBeDefined();
      expect(bundle!.strategy_candidate_id).toBe(c.candidate_id);
      expect(bundle!.draft_index).toBe(c.draft_index);
    }
  });

  it("GET /knowledge/{id} extra fixture is a real knowledge.v1 (a second, provisional object)", () => {
    const extra = loadFixture<{ key: string; status: string; status_history: { changed_at: string }[] }>("knowledge.extra.json");
    expect(validateSchema("knowledge", extra).errors).toEqual([]);
    expect(extra.status).toBe("provisional");
    expect(extra.status_history).toHaveLength(2);
  });

  it("the newest agent action is the executed send the snapshot reads, and the cited customer reply exists", () => {
    const timeline = loadFixture<{ items: { id: string; activity_type: string; occurred_at: string }[] }>("acme.timeline.json");
    const newestAgentAction = timeline.items.find((a) => a.activity_type === "AgentActionExecuted")!;
    expect(validateSchema("activity", newestAgentAction).errors).toEqual([]);
    // It came after the human's send decision, and after the email that started the run.
    expect(newestAgentAction.occurred_at > "2026-09-29T15:42:00Z").toBe(true);
    // The customer_reaction in the run trace cites this reply — it must exist in the world.
    const reply = timeline.items.find((a) => a.id === "0ac70000-0000-4000-8000-000000000102");
    expect(reply?.activity_type).toBe("EmailReceived");
    expect(reply!.occurred_at > newestAgentAction.occurred_at).toBe(true);
  });

  it("the diff summary counts match its changes and every change targets a graph element or a removal", () => {
    const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
    const graph = loadFixture<Graph>("acme.graph.json");
    const count = (op: string) => diff.changes.filter((c) => c.op === op).length;
    expect(diff.summary).toEqual({ added: count("added"), changed: count("changed"), removed: count("removed"), repaired: count("repaired") });
    const ids = new Set([...graph.nodes.map((n) => n.id), ...graph.edges.map((e) => e.id)]);
    for (const change of diff.changes.filter((c) => c.op !== "removed")) expect(ids.has(change.id), change.id).toBe(true);
  });
});
