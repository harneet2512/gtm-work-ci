// HAR-145: gate cards grouped by Cliff message, the demo explainer, and the two views that must never mix: the current
// episode's protection (live and conditional gates in causal order) and System Proof (S2-S5 plus offline coverage).
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { buildGateCards, explainGate, type BucketSourceLite, type GateDefinitionSource } from "@/lib/evals/gate-cards";
import { episodeProtection, systemProof, EPISODE_ORDER } from "@/lib/evals/gate-scopes";

const REG = JSON.parse(readFileSync(path.resolve(__dirname, "../../contracts/evals/eval_registry.json"), "utf8")) as { gates: GateDefinitionSource[]; buckets: BucketSourceLite[] };
const groups = buildGateCards(REG.gates, REG.buckets, []);
const cards = groups.flatMap((g) => g.cards);

describe("cards grouped by message", () => {
  it("lead with M1, M2, M3, EcoLite Play and System Trust last, from the registry's message field", () => {
    expect(groups.map((g) => g.message)).toEqual(["M1", "M2", "M3", "ecolite", "system"]);
    expect(groups.map((g) => g.cards.map((c) => c.id).join(","))).toEqual(["B1,B2,B3,B4,B5,B6,B8", "D1,D2,D3,D4,D7,D8,D9", "B9,D5,D6,D10", "B7", "S1,S2,S3,S4,S5,S6"]);
  });
  it("keep the bucket as a secondary label and the message as a chip", () => {
    const b9 = cards.find((c) => c.id === "B9")!;
    expect(b9.bucket).toBe("context");
    expect(b9.message).toBe("M3");
    expect(b9.bucketLabel).toBe("Context");
  });
  it("show S2 as Eval Trust", () => {
    expect(cards.find((c) => c.id === "S2")!.name).toBe("Eval Trust");
  });
});

describe("explainGate", () => {
  it("says what it is, when it runs and what failure changes, from the registry", () => {
    const d8 = explainGate(cards.find((c) => c.id === "D8")!);
    expect(d8.what).toContain("about to go out");
    expect(d8.when).toBe("Live · immediately before Send");
    expect(d8.changes).toBe("blocks current action");
  });
  it("reads an offline gate's cadence and a monitoring-only gate's impact", () => {
    const s3 = explainGate(cards.find((c) => c.id === "S3")!);
    expect(s3.when).toBe("Offline · repeated-trial evaluation");
    expect(s3.changes).toBe("monitoring only");
  });
});

describe("episode protection (What protected this decision?)", () => {
  const view = episodeProtection(cards);
  it("lists only live and conditional gates in HAR-145's causal order", () => {
    expect(EPISODE_ORDER.map((p) => p.phase)).toEqual(["INGEST", "DECIDE", "HUMAN", "SEND"]);
    expect(view.map((p) => p.gates.map((g) => g.id + (g.conditional ? "?" : "")).join(" "))).toEqual(["B1 B2 B3 B4 B7? B8", "D1 D2 D3", "D4? D5? D6? D7?", "D8 D9 D10?"]);
  });
  it("never includes an offline or continuous gate", () => {
    const ids = view.flatMap((p) => p.gates.map((g) => g.id));
    expect(ids.some((id) => ["S1", "S2", "S3", "S4", "S5", "S6"].includes(id))).toBe(false);
  });
  it("gives a conditional gate the reason it did not run", () => {
    const d5 = view.flatMap((p) => p.gates).find((g) => g.id === "D5")!;
    expect(d5.skipReason).toBe("no human edit");
  });
});

describe("System Proof (How do we know the eval system is trustworthy?)", () => {
  const proof = systemProof(cards);
  it("holds S2 to S5 and one offline coverage row per B and D gate", () => {
    expect(proof.trust.map((g) => g.id)).toEqual(["S2", "S3", "S4", "S5"]);
    expect(proof.coverage.map((g) => g.id)).toEqual([...Array.from({ length: 9 }, (_, i) => `B${i + 1}`), ...Array.from({ length: 10 }, (_, i) => `D${i + 1}`)]);
  });
  it("says not measured where the backend exposes no count or last-run date", () => {
    for (const row of proof.coverage) expect(row).toMatchObject({ cases: "not measured", lastRun: "not measured" });
    expect(proof.calibration).toBe("not yet calibrated");
  });
});
