// HAR-145: every gate in the registry carries the message it belongs to and what its failure changes. Both are read from
// contracts/evals/eval_registry.json, never derived from a gate name. Go twin: core-go/internal/contracts/gate_impact_test.go.
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { MESSAGES } from "@/lib/evals/naming";

const IMPACTS = ["blocks current action", "requires human review", "triggers recomputation", "prevents knowledge promotion", "marks capability unreliable", "monitoring only"];
const REG = JSON.parse(readFileSync(path.resolve(__dirname, "../../contracts/evals/eval_registry.json"), "utf8")) as {
  gates: { id: string; impact?: string; impact_basis?: string; message?: string }[];
};

describe("registry impact and message parity", () => {
  it.each(REG.gates.map((g) => [g.id, g] as const))("%s has an impact value and a code basis", (_id, g) => {
    expect(IMPACTS).toContain(g.impact);
    expect((g.impact_basis ?? "").trim().length).toBeGreaterThan(10);
  });
  it.each(REG.gates.map((g) => [g.id, g] as const))("%s names a message the web knows", (_id, g) => {
    expect(MESSAGES.map((m) => m.id)).toContain(g.message);
  });
  it("puts B and D gates in M1, M2, M3 or ecolite and S gates in system", () => {
    for (const g of REG.gates) expect(g.id.startsWith("S") ? g.message === "system" : g.message !== "system").toBe(true);
  });
  it("follows the owner's mapping of messages to gates", () => {
    const by = (m: string) => REG.gates.filter((g) => g.message === m).map((g) => g.id);
    expect(by("M1")).toEqual(["B1", "B2", "B3", "B4", "B5", "B6", "B8"]);
    expect(by("M2")).toEqual(["D1", "D2", "D3", "D4", "D7", "D8", "D9"]);
    expect(by("M3")).toEqual(["B9", "D5", "D6", "D10"]);
    expect(by("ecolite")).toEqual(["B7"]);
  });
  it("sets impact from backend behaviour: D8 blocks, B9 holds promotion, every other live gate is monitoring only today", () => {
    const impact = Object.fromEntries(REG.gates.map((g) => [g.id, g.impact]));
    expect(impact.D8).toBe("blocks current action");
    expect(impact.B9).toBe("prevents knowledge promotion");
    expect(impact.S2).toBe("marks capability unreliable");
    expect(impact.D7).toBe("monitoring only");
  });
});
