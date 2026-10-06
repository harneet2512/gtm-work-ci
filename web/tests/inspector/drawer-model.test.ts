import { describe, expect, it } from "vitest";
import type { EvidenceItem } from "@/lib/evals/gate-table";
import { buildDrawerModel, DRAWER_SECTIONS, type DrawerContext } from "@/lib/evals/inspector/drawer-model";
import { CAND_A, CAND_B, def, EP, result, RULES } from "./fixtures";

const resolve = (ref: string): EvidenceItem => {
  const i = ref.indexOf(":");
  const kind = ref.slice(0, i);
  const id = ref.slice(i + 1);
  return { ref, kind, id, label: `${kind} ${id.slice(0, 8)}`, summary: kind === "activity" ? "We can sign next week if legal clears the DPA." : null, spanId: null, href: null, source: kind === "activity" ? "Email · Nov 9, 2023" : null };
};

function ctx(over: Partial<DrawerContext> = {}): DrawerContext {
  const d = over.def ?? def("D3", { grader: "model" });
  return {
    episodeId: EP,
    episodeLabel: "MedTech · Nov 9, 2023",
    def: d,
    results: over.results ?? [result({ gate: "D3", sub_gate: "ranking", verdict: "warn", grader: { kind: "model", model: "m", prompt_version: "ranking:v1" }, evidence_refs: [`candidate:${CAND_A}`, "activity:0e7a1000-0000-4000-8000-0000000000a1"], criteria: [{ id: "supported_by_evidence_state", label: "", result: "pass", why: "ok", evidence_refs: ["activity:x"] }, { id: "uncertainty_reflected", label: "", result: "warn", why: "uncertainty is understated", evidence_refs: ["activity:x"] }], latency_ms: 812, model_calls: 1 })],
    rules: RULES,
    resolve,
    titleOf: (id) => (id === CAND_A ? "Ask for the DPA review date" : id === CAND_B ? "Wait for legal" : null),
    extraInputs: [],
    fleet: [],
    confidence: null,
    ...over,
  };
}

describe("drawer model (HAR-149 section 3)", () => {
  it("has the nine sections in HAR-149's order", () => {
    expect(DRAWER_SECTIONS.map((s) => s.id)).toEqual(["what", "why", "when", "inputs", "how", "happened", "effect", "performance", "failures"]);
    expect(DRAWER_SECTIONS.map((s) => s.title)).toEqual([
      "What are we doing?",
      "Why do we care?",
      "When does this run?",
      "What went into it?",
      "How was the result produced?",
      "What happened in this run?",
      "What did this result do?",
      "How is it performing over time?",
      "Common failures",
    ]);
  });

  it("leads with the human question and takes the plain words from the registry", () => {
    const m = buildDrawerModel(ctx());
    expect(m.question).toBe("Is D3 right?");
    expect(m.what).toBe("We check what D3 does, in plain words.");
    expect(m.why.text).toBe("If D3 fails, something bad slips through.");
    expect(m.why.invariant).toBe("A bad behaviour");
  });

  it("says when it runs: bucket, mode and trigger", () => {
    const m = buildDrawerModel(ctx());
    expect(m.when.bucket).toMatch(/are we doing the right thing/);
    expect(m.when.modeBadge).toBe("Live");
    expect(m.when.trigger).toBe("when a recommendation is ranked");
  });

  it("resolves the inputs to readable content, keeping raw JSON only under advanced", () => {
    const m = buildDrawerModel(ctx());
    const kinds = m.inputs.items.map((i) => i.kind);
    expect(kinds).toContain("option");
    expect(kinds).toContain("source");
    const option = m.inputs.items.find((i) => i.kind === "option")!;
    expect(option.title).toBe("Ask for the DPA review date");
    const email = m.inputs.items.find((i) => i.kind === "source")!;
    expect(email.text).toBe("We can sign next week if legal clears the DPA.");
    expect(email.source).toBe("Email · Nov 9, 2023");
    expect(m.inputs.advancedJson).toContain('"gate": "D3"');
    expect(JSON.stringify(m.inputs.items)).not.toContain('"gate":');
  });

  it("how: grader, one row per criterion and the folded result", () => {
    const m = buildDrawerModel(ctx());
    expect(m.how.grader).toMatchObject({ kind: "model", model: "m", promptVersion: "ranking:v1", calibrated: false });
    expect(m.how.criteria.groups[0]!.rows.map((r) => [r.label, r.result])).toEqual([
      ["Supported by the evidence and state", "pass"],
      ["Uncertainty is reflected", "warn"],
    ]);
    expect(m.how.criteria.result).toBe("warn");
    expect(m.how.criteria.score).toBeNull();
    expect(m.how.latencyMs).toBe(812);
  });

  it("what happened: verdict, evidence, the failed checks and confidence only when given", () => {
    const m = buildDrawerModel(ctx());
    expect(m.happened.verdict).toBe("warn");
    expect(m.happened.failedChecks.map((c) => c.label)).toEqual(["Uncertainty is reflected"]);
    expect(m.happened.evidence.length).toBe(2);
    expect(m.happened.confidence).toBeNull();
    expect(buildDrawerModel(ctx({ confidence: 0.72 })).happened.confidence).toBe(0.72);
  });

  it("what did it do: a monitoring-only gate records only and cites the registry's basis", () => {
    const m = buildDrawerModel(ctx());
    expect(m.effect.effect).toBe("RECORD ONLY");
    expect(m.effect.hardStop).toBe(false);
    expect(m.effect.impact).toBe("monitoring only");
    expect(m.effect.basis).toMatch(/No code reads/);
  });

  it("performance: real counts over the other episodes; model gates say what the calibration run will measure", () => {
    const fleet = [
      { episodeId: "e1", label: "MedTech · Nov 9", results: [result({ gate: "D3", verdict: "pass", latency_ms: 1000 })] },
      { episodeId: "e2", label: "Acme · Oct 2", results: [result({ gate: "D3", verdict: "fail" })] },
      { episodeId: "e3", label: "Other", results: [result({ gate: "D1", verdict: "pass" })] },
    ];
    const m = buildDrawerModel(ctx({ fleet }));
    expect(m.performance.distribution).toEqual({ pass: 1, warn: 0, fail: 1, unknown: 0, total: 2 });
    expect(m.performance.episodes).toBe(2);
    const fp = m.performance.metrics.find((x) => x.id === "false_pass")!;
    expect(fp.state).toBe("not_measured");
    expect(fp.note).toMatch(/Not measured yet: needs the calibration run/);
    expect(fp.measures).toMatch(/passed something it should have failed/i);
    expect(m.performance.metrics.find((x) => x.id === "agreement")!.note).toMatch(/not human/i);
    expect(m.performance.metrics.find((x) => x.id === "latency")!).toMatchObject({ state: "measured", value: "1.0 s" });
  });

  it("performance: a deterministic gate does not list model-grader metrics", () => {
    const m = buildDrawerModel(ctx({ def: def("D7", { grader: "deterministic" }), results: [result({ gate: "D7" })] }));
    expect(m.performance.metrics.map((x) => x.id)).not.toContain("false_pass");
    expect(m.performance.metrics.map((x) => x.id)).not.toContain("agreement");
  });

  it("performance: with no data it says so and invents nothing", () => {
    const m = buildDrawerModel(ctx({ fleet: [] }));
    expect(m.performance.distribution).toEqual({ pass: 0, warn: 0, fail: 0, unknown: 0, total: 0 });
    expect(m.performance.episodes).toBe(0);
  });

  it("common failures: real categories from stored results with example episodes, else an honest empty state", () => {
    const failing = result({ gate: "D3", verdict: "fail", criteria: [{ id: "no_blocked_preferred", label: "", result: "fail", why: "a blocked option was recommended", evidence_refs: ["candidate:x"] }] });
    const m = buildDrawerModel(ctx({ fleet: [{ episodeId: "e2", label: "Acme · Oct 2", results: [failing] }, { episodeId: "e1", label: "MedTech", results: [result({ gate: "D3", verdict: "pass" })] }] }));
    expect(m.failures.categories).toHaveLength(1);
    expect(m.failures.categories[0]).toMatchObject({ label: "No blocked option is recommended", count: 1 });
    expect(m.failures.categories[0]!.examples[0]).toMatchObject({ episodeId: "e2", episodeLabel: "Acme · Oct 2" });
    expect(m.failures.empty).toBeNull();
    const none = buildDrawerModel(ctx({ fleet: [{ episodeId: "e1", label: "MedTech", results: [result({ gate: "D3", verdict: "pass" })] }] }));
    expect(none.failures.categories).toEqual([]);
    expect(none.failures.empty).toBe("No failures recorded yet");
  });

  it("a gate with no stored result in this episode says it has not run, and is not a pass", () => {
    const m = buildDrawerModel(ctx({ results: [] }));
    expect(m.measured).toBe(false);
    expect(m.happened.verdict).toBeNull();
    expect(m.happened.statement).toMatch(/No result has been recorded/);
    expect(m.how.criteria.groups).toEqual([]);
  });

  it("extra inputs (the stored ranking, the edit) are shown beside the evidence", () => {
    const m = buildDrawerModel(ctx({ extraInputs: [{ kind: "ranking", title: "The stored ranking", text: "A ranked above B: it fits the state better.", source: null, href: null }] }));
    expect(m.inputs.items[0]!.kind).toBe("ranking");
  });
});
