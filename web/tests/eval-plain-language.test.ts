// HAR-149: every gate explains itself in plain language, and the control-effect rules the drawer shows are the
// registry's, never made up by the web.
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const registry = JSON.parse(readFileSync(path.join(process.cwd(), "..", "contracts", "evals", "eval_registry.json"), "utf8")) as {
  gates: { id: string; plain_what?: string; plain_why?: string; impact: string }[];
  control_effect_rules?: { vocabulary: string[]; default: Record<string, string>; gates: Record<string, Record<string, unknown>> };
};

describe("registry plain language (HAR-149)", () => {
  it("has all 25 gates", () => expect(registry.gates).toHaveLength(25));

  it.each(registry.gates.map((g) => [g.id, g] as const))("%s has plain_what and plain_why", (_id, g) => {
    expect(g.plain_what?.length ?? 0).toBeGreaterThan(40);
    expect(g.plain_why?.length ?? 0).toBeGreaterThan(40);
    expect(g.plain_what).not.toMatch(/\bGhost\b/);
    expect(g.plain_why).not.toMatch(/\bGhost\b/);
  });

  it("every control effect rule uses the vocabulary and agrees with the gate's impact", () => {
    const rules = registry.control_effect_rules!;
    const used = [...Object.values(rules.default), ...Object.values(rules.gates).flatMap((r) => Object.entries(r).filter(([k]) => k !== "basis" && k !== "only_for").map(([, v]) => v as string))];
    for (const e of used) expect(rules.vocabulary).toContain(e);
    // a gate whose impact is monitoring only never blocks
    for (const g of registry.gates.filter((x) => x.impact === "monitoring only")) {
      expect(Object.values(rules.gates[g.id] ?? {})).not.toContain("BLOCK");
    }
  });
});
