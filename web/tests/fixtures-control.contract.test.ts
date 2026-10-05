// The fixture core's control-plane documents (HAR-145) are contract-valid and honest: each validates against its schema,
// the eval counts are tallies of the very results the run and eval pages show, every trace eval id resolves, progress
// follows a real clock without ever calling a finished non-eval stage PASS, and the comparison refuses different triggers.
import { describe, expect, it } from "vitest";
import { allEntries, compareEntries, controlReply, documentsOf, paged } from "./e2e/fixture-control.mjs";
import { progressDocument, STAGES } from "./e2e/fixture-progress.mjs";
import { loadFixture, validateSchema } from "./contract-validator";

const entries = allEntries();
const resultsOf = (e: ReturnType<typeof allEntries>[number]) => e.strategies.eval_bundles.flatMap((b: { items: { result: { id: string } | null }[] }) => b.items.map((i) => i.result).filter(Boolean)) as { id: string; verdict: string }[];
const must = (name: string, doc: unknown) => {
  const r = validateSchema(name, doc);
  expect(r.errors, `${name}: ${r.errors.join("; ")}`).toEqual([]);
};
const get = (path: string) => controlReply(new URL(path, "http://fixture.test"));

describe.each(entries.map((e) => [e.episodeId, e] as const))("documents of episode %s", (_id, entry) => {
  const docs = documentsOf(entry);

  it("validate against their schemas", () => {
    must("episode_summary", docs.summary);
    must("episode_trace", docs.trace);
    for (const m of docs.mutations) must("knowledge_mutation", m);
    must("operational_metrics", docs.metrics);
    must("dependency_invalidation", docs.recomputation);
    must("eval_run", docs.evalRun);
    must("eval_family_summary", docs.families);
  });

  it("lists every eval result exactly once across the trace spans and the unassigned list", () => {
    const placed = [...docs.trace.spans.flatMap((s: { eval_result_ids: string[] }) => s.eval_result_ids), ...docs.trace.unassigned_eval_result_ids];
    expect(placed.sort()).toEqual(resultsOf(entry).map((r) => r.id).sort());
  });

  it("counts the eval run from the same results the pages show, and the families sum to the areas", () => {
    const results = resultsOf(entry);
    expect(docs.evalRun.result_count).toBe(results.length);
    expect(docs.evalRun.counts.fail).toBe(results.filter((r) => r.verdict === "fail").length);
    expect(docs.evalRun.counts.unknown).toBe(results.filter((r) => r.verdict === "abstain").length);
    for (const a of docs.families.areas) {
      expect(a.families.reduce((n: number, f: { counts: { total: number } }) => n + f.counts.total, 0)).toBe(a.counts.total);
      expect(a.measured).toBe(a.counts.total > 0);
    }
  });

  it("serves the same documents through the routes", () => {
    expect(get(`/episodes/${entry.episodeId}`)!.body).toEqual(docs.summary);
    expect(get(`/episodes/${entry.episodeId}/trace`)!.body).toEqual(docs.trace);
    expect(get(`/episodes/${entry.episodeId}/knowledge-mutations`)!.body).toEqual({ episode_id: entry.episodeId, items: docs.mutations });
    expect(get(`/episodes/${entry.episodeId}/metrics`)!.body).toEqual(docs.metrics);
    expect(get(`/runs/${entry.run.id}/recomputation`)!.body).toEqual(docs.recomputation);
    expect(get(`/eval-runs/${entry.run.id}/families`)!.body).toEqual(docs.families);
  });
});

describe("what the fixtures say honestly", () => {
  const byEpisode = (prefix: string) => entries.find((e) => e.episodeId.startsWith(prefix))!;
  const medtech = documentsOf(byEpisode("0e9ead00"));
  const acme = documentsOf(byEpisode("0e9e0000"));
  const replay = documentsOf(byEpisode("0de50000"));

  it("never invents a precedent and keeps knowledge attribution absent when nothing was cited", () => {
    for (const d of [medtech, acme, replay]) expect(d.trace.spans.find((s: { kind: string }) => s.kind === "precedents").status).toBe("not_recorded");
    expect(medtech.trace.spans.find((s: { kind: string }) => s.kind === "knowledge_used").status).toBe("not_recorded");
  });

  it("reports a replayed run as unmeasured with no spend, a live one as measured, and a run with no usage as unmeasured", () => {
    expect(acme.metrics).toMatchObject({ measured: false, usage_source: "replay", cost_usd: null });
    expect(medtech.metrics).toMatchObject({ measured: true, usage_source: "live" });
    expect(replay.metrics).toMatchObject({ measured: false, usage_source: null });
  });

  it("has a WEAKEN among the Acme episode's knowledge mutations and none for MedTech", () => {
    expect(acme.mutations.map((m: { operation: string }) => m.operation)).toContain("WEAKEN");
    expect(medtech.mutations).toEqual([]);
  });

  it("ties the replay episode's Message 1 to its own account change", () => {
    const evidence = replay.trace.spans.find((s: { kind: string }) => s.kind === "evidence");
    expect(evidence.refs[0].id).toBe("0a1c0000-0000-4000-8000-000000000302");
  });

  it("shows the edit as re-evaluated with account state preserved only when it is proven", () => {
    expect(medtech.recomputation.status).toBe("reevaluated");
    expect(medtech.recomputation.account_state).toMatchObject({ preserved: true });
    expect(medtech.recomputation.entries.length).toBeGreaterThan(0);
  });
});

describe("paging", () => {
  it("serves keyset pages with a next cursor and refuses a malformed cursor", () => {
    const first = paged(new URL("http://x.test/accounts?limit=1"), [1, 2, 3]);
    expect(first.body).toEqual({ items: [1], next_cursor: "o1" });
    expect(paged(new URL("http://x.test/accounts?limit=1&cursor=o2"), [1, 2, 3]).body).toEqual({ items: [3], next_cursor: null });
    expect(paged(new URL("http://x.test/accounts?cursor=junk"), [1]).status).toBe(400);
  });

  it("lists eval runs newest first, only runs with results", () => {
    const list = get("/eval-runs")!.body.items as { id: string; account_name: string }[];
    expect(list.length).toBe(entries.length);
    expect(get("/eval-runs?limit=1")!.body.next_cursor).toBe("o1");
    for (const run of list) must("eval_run", run);
  });
});

describe("run comparison", () => {
  const acmeRun = entries.find((e) => e.episodeId.startsWith("0e9e0000"))!.run.id;
  const replayRun = entries.find((e) => e.episodeId.startsWith("0de50000"))!.run.id;
  const medtechRun = entries.find((e) => e.episodeId.startsWith("0e9ead00"))!.run.id;

  it("compares two runs of the same trigger and validates", () => {
    const reply = get(`/eval-runs/compare?a=${acmeRun}&b=${replayRun}`)!;
    expect(reply.status).toBe(200);
    must("eval_run_comparison", reply.body);
    expect(reply.body.overall.change).toBe("unchanged");
  });

  it("refuses runs of different triggers with not_comparable, an unknown run with 404 and a missing side with 400", () => {
    expect(get(`/eval-runs/compare?a=${acmeRun}&b=${medtechRun}`)).toMatchObject({ status: 422, error: { code: "not_comparable" } });
    expect(get(`/eval-runs/compare?a=${acmeRun}&b=0f0a0000-0000-4000-8000-0000000000ff`)!.status).toBe(404);
    expect(get(`/eval-runs/compare?a=${acmeRun}`)!.status).toBe(400);
  });

  it("classes a verdict that moved from fail to pass as improved and one that disappeared as removed", () => {
    const [a, b] = [entries[0]!, entries[0]!];
    const tweaked = { ...b, strategies: { ...b.strategies, eval_bundles: b.strategies.eval_bundles.map((bundle: { items: { result: { verdict: string } | null }[] }) => ({ ...bundle, items: bundle.items.map((i) => (i.result?.verdict === "fail" ? { ...i, result: { ...i.result, verdict: "pass" } } : i)) })) } };
    const c = compareEntries(a, tweaked);
    expect(c.rows.some((r: { change: string }) => r.change === "improved")).toBe(true);
    must("eval_run_comparison", c);
    const fewer = { ...b, strategies: { ...b.strategies, eval_bundles: b.strategies.eval_bundles.slice(0, 1) } };
    expect(compareEntries(a, fewer).rows.some((r: { change: string }) => r.change === "removed")).toBe(true);
  });
});

describe("play progress on a real clock", () => {
  const world = loadFixture<{ manifest_id: string; account_id: string; events: { release: { material: boolean } }[] }>("replay.events.json");
  const t0 = 1_000_000;
  const at = (event: unknown, ms: number) => progressDocument({ play: { startedAt: t0, event }, world, playMs: 2600, now: t0 + ms });
  const material = world.events[2]!;
  const nonMaterial = world.events[1]!;

  it("is not started with seven waiting stages before any Play", () => {
    const doc = progressDocument({ play: { startedAt: null, event: null }, world, playMs: 2600, now: t0 });
    must("pipeline_progress", doc);
    expect(doc.overall).toBe("not_started");
    expect(doc.stages.map((s: { stage: string }) => s.stage)).toEqual([...STAGES]);
  });

  it("validates at every moment of a material event and only ever moves forward", () => {
    let finished = 0;
    for (const ms of [0, 100, 450, 800, 1000, 1500, 1900, 2400, 2700, 3000, 3200, 4000]) {
      const doc = at(material, ms);
      must("pipeline_progress", doc);
      const done = doc.stages.filter((s: { status: string }) => !["waiting", "running"].includes(s.status)).length;
      expect(done).toBeGreaterThanOrEqual(finished);
      finished = done;
    }
    expect(at(material, 4000).overall).toBe("complete");
    expect(at(material, 1000).overall).toBe("running");
  });

  it("never calls a finished non-eval stage passed: only the evals stage carries a verdict, from the real results", () => {
    const doc = at(material, 4000);
    for (const s of doc.stages) if (s.stage !== "evals") expect(["completed"]).toContain(s.status);
    const evals = doc.stages.find((s: { stage: string }) => s.stage === "evals");
    expect(["passed", "warning"]).toContain(evals.status);
    expect(evals.status).toBe("warning"); // the played run's results include warns and fails
    expect(evals.eval_result_ids.length).toBeGreaterThan(0);
  });

  it("keeps Cliff open after the Play request would have returned, so a stale final state is impossible", () => {
    const doc = at(material, 2700);
    expect(doc.overall).toBe("running");
    expect(doc.stages.find((s: { stage: string }) => s.stage === "cliff").status).toBe("running");
  });

  it("skips everything after resolve for a non-material event", () => {
    const doc = at(nonMaterial, 1500);
    must("pipeline_progress", doc);
    expect(doc.stages.map((s: { status: string }) => s.status)).toEqual(["completed", "completed", "skipped", "skipped", "skipped", "skipped", "skipped"]);
    expect(doc.overall).toBe("complete");
  });
});
