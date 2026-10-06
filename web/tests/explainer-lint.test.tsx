// The Explaining evals page is written for a person, not a registry: no eval ids, no snake_case, no code names and no old brand
// anywhere in what it renders (or in the contract copy behind it).
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ExplainerView } from "@/components/evals/explainer/ExplainerView";
import { buildExplainerPage, loadExplainer } from "@/lib/evals/explainer";
import { CONTRACTS_DIR } from "./contract-validator";

const explainer = loadExplainer(CONTRACTS_DIR);
const page = buildExplainerPage(explainer, new Set(["B1"]));
const html = renderToStaticMarkup(<ExplainerView page={page} />);
const text = html.replace(/<[^>]+>/g, " ").replace(/&#x27;|&#39;/g, "'").replace(/&quot;/g, '"').replace(/&amp;/g, "&");
// Everything a reader sees, plus the attribute values the DOM carries (ids, hrefs, test ids).
const copy = [
  ...Object.values(explainer.intro).flatMap((v) => (typeof v === "string" ? [v] : v.flatMap((r) => [r.label, r.meaning]))),
  ...Object.values(explainer.cadence_labels),
  ...Object.values(explainer.decides_by_labels).flatMap((l) => [l.label, l.meaning]),
  ...explainer.moments.flatMap((m) => [m.kicker, m.title, m.question, m.intro]),
  ...explainer.entries.flatMap((e) => [e.human_name, e.what, e.why, e.example, e.when, e.result_effect]),
].join(" | ");

const LEAKS: readonly [string, RegExp][] = [
  ["an eval id", /\b[BDS]\d{1,2}\b/],
  ["a message id", /\bM[123]\b/],
  ["a snake_case name", /\b(?!gtm_ai\b)[a-z]+(?:_[a-z0-9]+)+\b/],
  ["a camelCase code name", /\b[a-z]+[A-Z][A-Za-z]+\b/],
  ["the old brand", /\bGhost\b/],
  ["registry jargon", /\b(bucket|gate id|deterministic|hybrid|EvalBundle|grader|span|episode id)\b/i],
];

describe("Explaining evals wording", () => {
  it("renders all 29 cards and five sections", () => {
    expect(html.match(/data-testid="explainer-card"/g)).toHaveLength(29);
    expect(html.match(/data-testid="explainer-section"/g)).toHaveLength(5);
  });

  it.each(LEAKS)("leaks no %s into the rendered text", (_name, re) => {
    expect(text.match(re)?.[0] ?? null).toBeNull();
  });

  it.each(LEAKS)("leaks no %s into the contract copy", (_name, re) => {
    expect(copy.match(re)?.[0] ?? null).toBeNull();
  });

  it("puts no eval id in an id, link or class either", () => {
    const attrs = [...html.matchAll(/\s(?:id|href|class|data-[a-z-]+)="([^"]*)"/g)].map((m) => m[1]!);
    for (const a of attrs) expect(a, a).not.toMatch(/\b[BDS]\d{1,2}\b/);
  });

  it("gives every card a human question as its title", () => {
    for (const s of page.sections) for (const c of s.cards) {
      expect(c.humanName.length, c.humanName).toBeGreaterThan(10);
      expect(c.humanName).toMatch(/[?]$/);
    }
  });

  it("says the brand is gtm_ai", () => {
    expect(text).toContain("gtm_ai");
    expect(path.basename(CONTRACTS_DIR)).toBe("contracts");
  });
});
