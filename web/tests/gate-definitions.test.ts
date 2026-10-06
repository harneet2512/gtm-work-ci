// The card model and the registry parity: every B, D and S gate carries its HAR-97 v2 definition in the contract, and the
// cards read it without inventing anything. Go twin: core-go/internal/contracts/gate_definitions_test.go.
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { buildGateCards, CHAIN, MODES, definitionsById, exampleFor, GATE_STEPS, momentOf, runsHint, gateFromParam, normalizeGrader, type BucketSourceLite, type GateDefinitionSource } from "@/lib/evals/gate-cards";
import { buildGateRows, type EpisodeInput } from "@/lib/evals/gate-table";

const REG = JSON.parse(readFileSync(path.resolve(__dirname, "../../contracts/evals/eval_registry.json"), "utf8")) as { gates: GateDefinitionSource[]; buckets: BucketSourceLite[] };

describe("registry parity", () => {
  it("has exactly the gates B1-B9, D1-D10, S1-S6", () => {
    const want = [...Array.from({ length: 9 }, (_, i) => `B${i + 1}`), ...Array.from({ length: 10 }, (_, i) => `D${i + 1}`), ...Array.from({ length: 6 }, (_, i) => `S${i + 1}`)];
    expect(REG.gates.map((g) => g.id)).toEqual(want);
  });
  it.each(REG.gates.filter((g) => g.id[0] !== "S").map((g) => [g.id, g] as const))("%s carries its full definition", (_id, g) => {
    for (const f of ["display_name", "display_question", "invariant", "judges", "grader", "protects"] as const) expect(g[f], f).toBeTruthy();
    expect(normalizeGrader(g.grader), "grader").not.toBeNull();
    expect(Object.keys(g.criteria ?? {}).length > 0 || Boolean(g.criteria_note), "criteria").toBe(true);
    if (g.id[0] === "D") expect(g.when, "when").toBeTruthy();
  });
  it.each(REG.gates.filter((g) => g.id[0] === "S").map((g) => [g.id, g] as const))("%s says what is built today", (_id, g) => {
    expect(g.display_name).toBeTruthy();
    expect(g.status_note).toBeTruthy();
  });
  it.each(REG.gates.map((g) => [g.id, g] as const))("%s has an execution mode and a trigger (HAR-97)", (_id, g) => {
    expect(["live_required", "live_conditional", "offline_benchmark", "continuous_aggregate"]).toContain(g.mode);
    expect(g.trigger).toBeTruthy();
    expect(Boolean(g.not_triggered)).toBe(g.mode === "live_conditional");
  });
  it("follows HAR-97's table: S1 and S6 continuous, S2-S5 offline, B5/B6/B7/B9 and D4-D7/D10 conditional", () => {
    const mode = Object.fromEntries(REG.gates.map((g) => [g.id, g.mode]));
    expect(["S1", "S6"].map((id) => mode[id])).toEqual(["continuous_aggregate", "continuous_aggregate"]);
    expect(["S2", "S3", "S4", "S5"].every((id) => mode[id] === "offline_benchmark")).toBe(true);
    expect(["B5", "B6", "B7", "B9", "D4", "D5", "D6", "D7", "D10"].every((id) => mode[id] === "live_conditional")).toBe(true);
    expect(["B1", "B2", "B3", "B4", "B8", "D1", "D2", "D3", "D8", "D9"].every((id) => mode[id] === "live_required")).toBe(true);
  });
  it("never invents a verdict criterion: only pass, warn, fail and unknown, all non-empty", () => {
    for (const g of REG.gates) for (const [k, v] of Object.entries(g.criteria ?? {})) {
      expect(["pass", "warn", "fail", "unknown"]).toContain(k);
      expect(v.trim()).not.toBe("");
    }
  });
});

const ep = (results: unknown[]): EpisodeInput => ({
  episode: { id: "e1", label: "E", accountId: "a" },
  results: results as never,
  spanTimes: new Map(),
  resolve: (ref) => ({ ref, kind: "k", id: ref, label: ref, summary: null, spanId: null, href: null }),
});
const res = (gate: string, verdict: string) => ({ gate, sub_gate: "", label: "", judged_object: { type: "X", id: gate + verdict }, span_id: "", verdict, question: "q", observed: "o", why: "w", evidence_refs: ["r"], improves: "i", grader: { kind: "model" }, calibrated: false });

describe("buildGateCards", () => {
  const rows = buildGateRows([ep([res("D2", "warn"), res("D2", "pass"), res("B1", "pass")])], REG.gates.map((g) => ({ id: g.id, question: g.question, improves: g.improves })));
  const buckets = buildGateCards(REG.gates, REG.buckets, rows);

  it("groups cards by message in loop order, then System Trust", () => {
    expect(buckets.map((b) => b.message)).toEqual(["M1", "M2", "M3", "ecolite", "system"]);
    expect(buckets.map((b) => b.cards.length)).toEqual([7, 7, 4, 1, 6]);
    expect(buckets[0]!.title).toBe("Message 1 · What changed");
  });
  it("uses the card name and question from the definitions", () => {
    const b1 = buckets[0]!.cards[0]!;
    expect(b1.name).toBe("Evidence fidelity");
    expect(b1.question).toBe("Did gtm_ai record exactly what happened in the new event?");
  });
  it("shows the latest real result of a gate, and null when it has none", () => {
    const d2 = buckets[1]!.cards.find((c) => c.id === "D2")!;
    expect(d2.example).toMatchObject({ verdict: "warn", observed: "o", episode: "E" });
    expect(buckets[1]!.cards.find((c) => c.id === "D3")!.example).toBeNull();
  });
  it("prefers the MedTech episode and carries one evidence summary with its source", () => {
    const evidence = { ref: "activity:a", kind: "activity", id: "a", label: "activity a", summary: "A clear understanding of future financial commitments is crucial.", spanId: null, href: null, source: "Email · Nov 9, 2023" };
    const medtech = { ...ep([res("B1", "pass")]), episode: { id: "m", label: "MedTech Advances", accountId: "a" }, resolve: () => evidence };
    const other = ep([res("B1", "fail")]);
    const rs = buildGateRows([other, medtech], REG.gates.map((g) => ({ id: g.id, question: g.question, improves: g.improves })));
    expect(exampleFor("B1", rs)).toMatchObject({ verdict: "pass", source: "Email · Nov 9, 2023", summary: expect.stringContaining("financial commitments") });
  });
  it("maps every B and D gate to chain steps that exist, and S gates to none", () => {
    for (const g of REG.gates) {
      if (g.id[0] === "S") expect(GATE_STEPS[g.id]).toBeUndefined();
      else {
        expect(GATE_STEPS[g.id], g.id).toBeTruthy();
        for (const s of GATE_STEPS[g.id]!) expect(CHAIN).toContain(s);
      }
    }
  });
  it("words the moments as the walkthrough does, and says when a gate runs instead of inventing a result", () => {
    expect(momentOf("D2", "Play (M2)")).toEqual({ label: "Play · Cliff M2", cliff: true });
    expect(momentOf("D9", "After send")).toEqual({ label: "After send", cliff: false });
    expect(momentOf("B3", undefined)).toEqual({ label: "Play", cliff: false });
    expect(momentOf("S1", undefined)).toBeNull();
    expect(runsHint({ label: "Play", cliff: false })).toBe("Runs when you press Play");
    expect(runsHint({ label: "After Cliff M3", cliff: true })).toBe("Runs at After Cliff M3");
    expect(runsHint(null)).toBe("not measured");
  });
  it("marks model and hybrid gates as not yet calibrated, deterministic ones not", () => {
    const by = Object.fromEntries(buckets.flatMap((b) => b.cards).map((c) => [c.id, c]));
    expect(by.B2!.grader).toBe("deterministic");
    expect(by.B2!.notCalibrated).toBe(false);
    expect(by.B1!.grader).toBe("hybrid");
    expect(by.B1!.notCalibrated).toBe(true);
    expect(by.D2!.grader).toBe("model");
  });
  it("shows S gates with their status note and no invented criteria", () => {
    const s2 = buckets[4]!.cards.find((c) => c.id === "S2")!;
    expect(s2.statusNote).toMatch(/not yet calibrated/);
    expect(Object.values(s2.criteria).every((v) => v === null)).toBe(true);
    expect(s2.grader).toBeNull();
  });
  it("leaves a criterion the definitions do not give as null", () => {
    expect(buckets[1]!.cards.find((c) => c.id === "D4")!.criteria.warn).toBeNull();
  });
});

describe("definitionsById and gateFromParam", () => {
  it("indexes the invariant and criteria by gate", () => {
    expect(definitionsById(REG.gates).B2!.criteria.fail).toMatch(/another account/);
  });
  it("accepts a known gate id in any case and ignores everything else", () => {
    const known = REG.gates.map((g) => g.id);
    expect(gateFromParam("d2", known)).toBe("D2");
    expect(gateFromParam("D99", known)).toBeNull();
    expect(gateFromParam("<script>", known)).toBeNull();
    expect(gateFromParam(undefined, known)).toBeNull();
  });
});

describe("H1: a gate never reads PASS while one of its results failed", () => {
  const catalog = REG.gates.map((g) => ({ id: g.id, question: g.question, improves: g.improves }));
  const mk = (verdicts: string[], label = "E", id = "e1") =>
    ({ ...ep(verdicts.map((v, i) => ({ ...res("D2", v), judged_object: { type: "X", id: `c${i}` } }))), episode: { id, label, accountId: "a" } });

  it("shows FAIL when a PASS precedes a FAIL in route order", () => {
    expect(exampleFor("D2", buildGateRows([mk(["pass", "fail"])], catalog))!.verdict).toBe("fail");
  });
  it("orders fail, warn, unknown, pass", () => {
    expect(exampleFor("D2", buildGateRows([mk(["pass", "unknown", "warn"])], catalog))!.verdict).toBe("warn");
    expect(exampleFor("D2", buildGateRows([mk(["pass", "unknown"])], catalog))!.verdict).toBe("unknown");
  });
  it("judges within one episode, the preferred one first: another episode's FAIL does not change a MedTech PASS", () => {
    const rs = buildGateRows([mk(["fail"], "Acme", "e2"), mk(["pass"], "MedTech Advances", "e1")], catalog);
    expect(exampleFor("D2", rs)!.verdict).toBe("pass");
    expect(exampleFor("D2", rs, "Acme")!.verdict).toBe("fail");
  });
});

describe("cards: execution modes and the chain", () => {
  const buckets = buildGateCards(REG.gates, REG.buckets, []);
  const by = Object.fromEntries(buckets.flatMap((b) => b.cards).map((c) => [c.id, c]));
  it("carries each gate's mode, trigger and, for conditional gates, the not-triggered condition", () => {
    expect(by.D5!.mode).toBe("live_conditional");
    expect(by.D5!.notTriggered).toBe("no human edit");
    expect(by.D5!.trigger).toBe("when a human edits the action");
    expect(by.B1!.notTriggered).toBeNull();
    expect(by.S2!.mode).toBe("offline_benchmark");
    expect(by.S1!.mode).toBe("continuous_aggregate");
  });
  it("uses gtm_ai in the demo-facing mode lines", () => {
    expect(JSON.stringify(MODES)).not.toContain("Ghost");
    expect(MODES.live_required.line).toContain("gtm_ai");
  });
  it("keeps the moment wording from the registry (After Cliff M3, not Cliff M3)", () => {
    expect(momentOf("D10", "After M3")!.label).toBe("After Cliff M3");
    expect(momentOf("D2", "Play (M2)")!.label).toBe("Play · Cliff M2");
  });
  it("marks Precedents and Execution on their own steps", () => {
    expect(GATE_STEPS.B5).toEqual(["Precedents"]);
    expect(GATE_STEPS.D9).toEqual(["Execution"]);
  });
});
