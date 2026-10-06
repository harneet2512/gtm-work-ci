// The gtm_ai tokens are the only colors the UI uses, so their contrast is checked here once for every
// text/background pair a surface renders (WCAG 2.2 AA: 4.5:1 for text, 3:1 for icons, focus and marks).
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { contrastRatio, parseTokens } from "./design-tokens";

const css = readFileSync(path.resolve(__dirname, "../app/styles/tokens.css"), "utf8");
const light = parseTokens(css, ":root");
const dark = parseTokens(css, '[data-theme="dark"]');

const ratio = (tokens: Record<string, string>, fg: string, bg: string): number => {
  const a = tokens[fg] ?? light[fg];
  const b = tokens[bg] ?? light[bg];
  if (!a || !b) throw new Error(`missing token ${a ? bg : fg}`);
  return contrastRatio(a, b);
};

const TEXT_ON: readonly [string, string][] = [
  ["--ink", "--surface"],
  ["--ink", "--canvas"],
  ["--ink-2", "--surface"],
  ["--ink-2", "--canvas"],
  ["--muted", "--surface"],
  ["--muted", "--canvas"],
  ["--muted", "--surface-sunken"],
  ["--brand-text", "--surface"],
  ["--brand-text", "--brand-tint"],
  ["--on-brand", "--brand"],
  ["--human", "--human-tint"],
  ["--fail", "--fail-tint"],
  ["--fail", "--surface"],
  ["--warn", "--warn-tint"],
  ["--warn", "--surface"],
  ["--pass", "--pass-tint"],
  ["--pass", "--surface"],
  ["--unsure", "--unsure-tint"],
  ["--nr", "--nr-tint"],
  ["--nr", "--surface"],
];

const MARKS_ON: readonly [string, string][] = [
  ["--brand", "--surface"],
  ["--fail-solid", "--surface"],
  ["--warn-solid", "--surface"],
  ["--pass-solid", "--surface"],
];

describe("design tokens", () => {
  it("parses the light and dark sets", () => {
    expect(Object.keys(light).length).toBeGreaterThan(30);
    expect(dark["--surface"]).toBeDefined();
  });

  it("the contrast check is not vacuous", () => {
    expect(contrastRatio("#ffffff", "#ffffff")).toBeCloseTo(1, 5);
    expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 5);
    expect(contrastRatio("#8a8f9c", "#ffffff")).toBeLessThan(4.5); // --faint is not a text color
  });

  it.each(TEXT_ON)("light: %s on %s is AA text (4.5:1)", (fg, bg) => {
    expect(ratio(light, fg, bg)).toBeGreaterThanOrEqual(4.5);
  });

  it.each(MARKS_ON)("light: %s on %s is AA for icons and focus (3:1)", (fg, bg) => {
    expect(ratio(light, fg, bg)).toBeGreaterThanOrEqual(3);
  });

  it.each(TEXT_ON.filter(([fg]) => fg !== "--on-brand"))("dark: %s on %s is AA text (4.5:1)", (fg, bg) => {
    expect(ratio(dark, fg, bg)).toBeGreaterThanOrEqual(4.5);
  });
});
