// The compact Judgment Delta: only the changed words, highlighted, short, with the full edit one toggle away.
import { describe, expect, it } from "vitest";
import { compactDiff, MAX_DIFF_CHARS } from "@/lib/view/edit-diff";

const text = (segs: { text: string }[]) => segs.map((s) => s.text).join("");

describe("compactDiff", () => {
  it("marks deleted and inserted words and keeps the unchanged words around them", () => {
    const d = compactDiff("Book 30 minutes this week", "Book 15 minutes this week");
    expect(d.segments.filter((s) => s.kind === "del").map((s) => s.text.trim())).toEqual(["30"]);
    expect(d.segments.filter((s) => s.kind === "ins").map((s) => s.text.trim())).toEqual(["15"]);
    expect(d.truncated).toBe(false);
  });
  it("shows only the changed sentence of a long paragraph, within the budget", () => {
    const same = "The first sentence is unchanged and rather long. ".repeat(8);
    const d = compactDiff(`${same}Please book a long call. ${same}`, `${same}Please book a short call. ${same}`);
    expect(text(d.segments).length).toBeLessThanOrEqual(MAX_DIFF_CHARS + 20);
    expect(d.segments.some((s) => s.kind === "ins" && s.text.includes("short"))).toBe(true);
    expect(d.truncated).toBe(true);
    expect(d.full).toEqual({ before: `${same}Please book a long call. ${same}`, after: `${same}Please book a short call. ${same}` });
  });
  it("is not truncated when nothing was cut", () => {
    expect(compactDiff("a b", "a c").truncated).toBe(false);
  });
  it("handles an empty side (pure insertion or deletion)", () => {
    expect(compactDiff("", "new text").segments.map((s) => s.kind)).toEqual(["ins"]);
    expect(compactDiff("old text", "").segments.map((s) => s.kind)).toEqual(["del"]);
  });
  it("reports no change for identical text", () => {
    expect(compactDiff("same", "same").segments).toEqual([]);
  });
});
