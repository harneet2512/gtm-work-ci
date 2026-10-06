// The Explaining evals model (HAR-97 eval list, HAR-149): one story in the order of the product loop, one card per eval.
import { describe, expect, it } from "vitest";
import { buildExplainerPage, loadExplainer } from "@/lib/evals/explainer";
import { CONTRACTS_DIR } from "./contract-validator";

const explainer = loadExplainer(CONTRACTS_DIR);
const REGISTRY = new Set(["B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9", "D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10", "S1", "S2", "S3", "S4", "S5", "S6"]);

describe("buildExplainerPage", () => {
  const page = buildExplainerPage(explainer, REGISTRY);

  it("orders the sections as the product loop and the health group last", () => {
    expect(page.sections.map((s) => s.id)).toEqual(["m1", "m2", "m3", "next_case", "health"]);
    expect(page.sections.map((s) => s.kicker)).toEqual(["Message 1", "Message 2", "Message 3", "The next similar case", "Keeping the evals honest"]);
  });

  it("gives every one of the 29 evals a card, in exactly one section", () => {
    const cards = page.sections.flatMap((s) => s.cards);
    expect(cards).toHaveLength(29);
    expect(new Set(cards.map((c) => c.key)).size).toBe(29);
  });

  it("marks only the evals the registry does not list yet as coming soon", () => {
    const soon = page.sections.flatMap((s) => s.cards).filter((c) => c.comingSoon).map((c) => c.key).sort();
    expect(soon).toEqual(["B10", "D11", "S7", "S8"]);
    const all = buildExplainerPage(explainer, new Set([...REGISTRY, "B10", "D11", "S7", "S8"]));
    expect(all.sections.flatMap((s) => s.cards).some((c) => c.comingSoon)).toBe(false);
  });

  it("keeps the story order inside Message 1: read, file, compare, what changed, whether to wake up, precedents, belief", () => {
    expect(page.sections[0]!.cards.map((c) => c.key)).toEqual(["B1", "B2", "B3", "B4", "B10", "B5", "B6", "B8"]);
  });

  it("puts the edit, the learning and the revision in Message 3 and knowledge use in the next case", () => {
    expect(page.sections[2]!.cards.map((c) => c.key)).toEqual(["D5", "D6", "D10", "B9"]);
    expect(page.sections[3]!.cards.map((c) => c.key)).toEqual(["B7", "D11"]);
  });

  it("words the badge and the way of deciding for the reader", () => {
    const b2 = page.sections[0]!.cards.find((c) => c.key === "B2")!;
    expect(b2.cadence).toBe("Every time");
    expect(b2.decidesBy.label).toBe("A rule");
    const d5 = page.sections[2]!.cards.find((c) => c.key === "D5")!;
    expect(d5.cadence).toBe("Only when triggered");
    expect(d5.decidesBy.label).toBe("A model judge");
  });

  it("states honestly that only the send-time check stops a send today", () => {
    const stops = page.sections.flatMap((s) => s.cards).filter((c) => /send is refused/i.test(c.resultEffect));
    expect(stops.map((c) => c.key)).toEqual(["D8"]);
  });

  it("introduces six results", () => {
    expect(page.intro.results.map((r) => r.label)).toEqual(["Passed", "Warning", "Failed", "Couldn't tell", "Didn't run", "Doesn't apply"]);
  });
});
