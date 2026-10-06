// Parity of contracts/evals/explainer.v1.json with the eval registry and HAR-97's list of 29 evals.
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { CONTRACTS_DIR, readJson, validateSchema } from "./contract-validator";

interface Entry { id: string; moment: string; cadence: string; decides_by: string }
interface Gate { id: string; message: string; mode: string; grader: string | null }
const explainer = readJson<{ entries: Entry[] }>(path.join(CONTRACTS_DIR, "evals", "explainer.v1.json"));
const registry = readJson<{ gates: Gate[] }>(path.join(CONTRACTS_DIR, "evals", "eval_registry.json"));

// HAR-97, FINAL EVAL SPEC section 0: the 29 evals. B10, D11, S7 and S8 are new there and may be pending in the registry.
const HAR97 = [
  ...Array.from({ length: 10 }, (_, i) => `B${i + 1}`),
  ...Array.from({ length: 11 }, (_, i) => `D${i + 1}`),
  ...Array.from({ length: 8 }, (_, i) => `S${i + 1}`),
];
const PENDING = new Set(["B10", "D11", "S7", "S8"]);
const MOMENT: Record<string, string> = { M1: "m1", M2: "m2", M3: "m3", ecolite: "next_case", system: "health" };
const CADENCE: Record<string, string> = { live_required: "every_time", live_conditional: "when_triggered", offline_benchmark: "offline", continuous_aggregate: "ongoing" };
const DECIDES: Record<string, string> = { deterministic: "rule", model: "judge", hybrid: "both" };

describe("explainer parity", () => {
  const byId = new Map(explainer.entries.map((e) => [e.id, e]));

  it("validates against its schema, and the committed example is the instance", () => {
    expect(validateSchema("eval_explainer", explainer)).toEqual({ valid: true, errors: [] });
    const example = readFileSync(path.join(CONTRACTS_DIR, "examples", "eval_explainer.example.json"), "utf8");
    expect(JSON.parse(example)).toEqual(explainer);
  });

  it("lists HAR-97's 29 evals exactly once each", () => {
    expect(HAR97).toHaveLength(29);
    expect(explainer.entries.map((e) => e.id).sort()).toEqual([...HAR97].sort());
  });

  it("has an entry for every registry gate", () => {
    for (const g of registry.gates) expect(byId.has(g.id), `no explainer entry for ${g.id}`).toBe(true);
  });

  it("has only entries the registry lists or HAR-97 defines, and pending ones are known", () => {
    const inRegistry = new Set(registry.gates.map((g) => g.id));
    for (const e of explainer.entries) expect(inRegistry.has(e.id) || HAR97.includes(e.id), e.id).toBe(true);
    for (const id of PENDING) expect(inRegistry.has(id) || byId.has(id)).toBe(true);
    for (const e of explainer.entries) if (!inRegistry.has(e.id)) expect(PENDING.has(e.id), `${e.id} is neither in the registry nor pending`).toBe(true);
  });

  it("agrees with the registry on the moment, how often it runs and how it decides (for gates the registry lists)", () => {
    for (const g of registry.gates.filter((x) => !PENDING.has(x.id))) {
      const e = byId.get(g.id)!;
      expect(e.moment, `${g.id} moment`).toBe(MOMENT[g.message]);
      expect(e.cadence, `${g.id} cadence`).toBe(CADENCE[g.mode]);
      if (g.grader) expect(e.decides_by, `${g.id} decides_by`).toBe(DECIDES[g.grader.split(" ")[0]!]);
    }
  });
});
