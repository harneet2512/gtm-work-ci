// Human-edit recomputation model (HAR-145): edits locate by verbatim before-text in the draft; an
// unlocatable edit is reported, never painted over the wrong words; shipped checks the sent artifact.
import { describe, expect, it } from "vitest";
import { locateEdits, placeSpans, spanBlocks } from "@/lib/view/edit-spans";
import type { HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import { loadFixture } from "./contract-validator";

const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const candidate = strategies.strategy_set.candidates.find((c) => c.candidate_id === decision.selected_candidate_id)!;
const original = (candidate.full_action_artifact as { body: string }).body;
const final = (decision.final_artifact as { body: string }).body;

describe("locateEdits", () => {
  const spans = locateEdits(original, final, decision.edits as { kind: string; before?: string; after?: string }[]);

  it("locates the recorded before-text in the draft", () => {
    const cta = spans.find((s) => s.kind === "cta_changed")!;
    expect(cta.at).toBeGreaterThan(0);
    expect(original.slice(cta.at, cta.at + cta.before!.length)).toBe(cta.before);
  });

  it("confirms the replacement is verbatim in the sent artifact", () => {
    expect(spans.find((s) => s.kind === "cta_changed")!.shipped).toBe(true);
  });

  it("marks edits with no before-text as unlocated rather than guessed", () => {
    expect(spans.find((s) => s.kind === "paragraph_edited")!.at).toBe(-1);
  });

  it("returns [] without a draft or edits", () => {
    expect(locateEdits(null, final, decision.edits as [])).toEqual([]);
    expect(locateEdits(original, final, [])).toEqual([]);
  });
});

describe("spanBlocks", () => {
  it("splits the draft into same-text and edited blocks in order", () => {
    const spans = locateEdits(original, final, decision.edits as { kind: string; before?: string; after?: string }[]);
    const blocks = spanBlocks(original, spans);
    const edited = blocks.filter((b) => b.type === "edited");
    expect(edited).toHaveLength(1); // only cta_changed has a locatable before
    const reassembled = blocks.map((b) => (b.type === "same" ? b.text! : b.span!.before!)).join("");
    expect(reassembled).toBe(original);
  });
});

describe("overlapping edits", () => {
  const body = "abcdefgh";
  const span = (before: string, at: number) => ({ kind: "k", before, after: "X", at, shipped: true });

  it("never duplicates draft text when two edits overlap", () => {
    const blocks = spanBlocks(body, [span("abcd", 0), span("cde", 2)]);
    const text = blocks.map((b) => (b.type === "same" ? b.text! : b.span!.before!)).join("");
    expect(text).toBe(body);
  });

  it("paints the earlier span and reports the overlapped one instead of drawing it", () => {
    const placed = placeSpans(body, [span("cde", 2), span("abcd", 0)]);
    expect(placed.painted.map((s) => s.before)).toEqual(["abcd"]);
    expect(placed.overlapped.map((s) => s.before)).toEqual(["cde"]);
  });

  it("lets adjacent spans both paint, and prefers the longer span when two start at the same offset", () => {
    expect(placeSpans(body, [span("ab", 0), span("cd", 2)]).overlapped).toEqual([]);
    const tie = placeSpans(body, [span("ab", 0), span("abcd", 0)]);
    expect(tie.painted.map((s) => s.before)).toEqual(["abcd"]);
    expect(tie.overlapped.map((s) => s.before)).toEqual(["ab"]);
  });

  it("ignores spans that were not located", () => {
    expect(placeSpans(body, [{ kind: "k", before: null, after: "X", at: -1, shipped: null }]).painted).toEqual([]);
  });
});
