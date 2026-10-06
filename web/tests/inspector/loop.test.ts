import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import type { GateDefinitionSource } from "@/lib/evals/gate-cards";
import { BUCKET_WORDS, bucketLine } from "@/lib/evals/inspector/buckets";
import { buildLoop, filterGates } from "@/lib/evals/inspector/loop-model";

const registry = JSON.parse(readFileSync(path.join(process.cwd(), "..", "contracts", "evals", "eval_registry.json"), "utf8")) as { gates: GateDefinitionSource[] };

describe("the loop landing (HAR-149 section 1)", () => {
  const loop = buildLoop(registry.gates);

  it("shows the three canonical buckets as human questions, in order", () => {
    expect(loop.buckets.map((b) => b.question)).toEqual([
      "Do we understand what is happening?",
      "Given what we know, are we doing the right thing?",
      "Can we trust the machinery measuring the first two?",
    ]);
    expect(loop.buckets.map((b) => b.gates.length)).toEqual([9, 10, 6]);
    expect(loop.buckets.map((b) => b.number)).toEqual([1, 2, 3]);
  });

  it("the flow goes Bucket 1 to Bucket 2 to execution, with feedback looping back to Bucket 1", () => {
    expect(loop.flow.map((f) => f.label)).toEqual(["Bucket 1", "Bucket 2", "Execution"]);
    expect(loop.feedback).toMatch(/feeds back into Bucket 1/i);
  });

  it("Live, Conditional, Offline and Continuous are modes (badges and filters), never buckets", () => {
    expect(loop.modes.map((m) => m.badge)).toEqual(["Live", "Conditional", "Offline", "Continuous"]);
    const all = loop.buckets.flatMap((b) => b.gates);
    expect(all.every((g) => ["live_required", "live_conditional", "offline_benchmark", "continuous_aggregate"].includes(g.mode))).toBe(true);
    expect(loop.modes.reduce((n, m) => n + m.count, 0)).toBe(25);
    expect(loop.buckets.map((b) => b.question).join(" ")).not.toMatch(/Live|Conditional|Offline|Continuous/);
  });

  it("each gate leads with its human question and keeps the id secondary", () => {
    const b5 = loop.buckets[0]!.gates.find((g) => g.id === "B5")!;
    expect(b5.question).toBe("Has a materially similar situation happened before here?");
    expect(b5.question).not.toBe("B5");
    const s6 = loop.buckets[2]!.gates.find((g) => g.id === "S6")!;
    expect(s6.mode).toBe("continuous_aggregate");
  });

  it("a mode filter keeps only that mode's gates", () => {
    const offline = filterGates(loop.buckets, "offline_benchmark");
    expect(offline.flatMap((b) => b.gates.map((g) => g.id))).toEqual(["S2", "S3", "S4", "S5"]);
    expect(filterGates(loop.buckets, "all").flatMap((b) => b.gates)).toHaveLength(25);
  });

  it("the bucket words pin the canonical questions used everywhere", () => {
    expect(BUCKET_WORDS).toHaveLength(3);
    expect(bucketLine("B1")).toBe("Bucket 1 · Do we understand what is happening?");
    expect(bucketLine("S4")).toBe("Bucket 3 · Can we trust the machinery measuring the first two?");
    expect(bucketLine("Z9")).toBe("");
  });
});
