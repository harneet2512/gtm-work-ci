// abstain is "gtm_ai is unsure" (UNKNOWN), not a warning: warn means a check found a problem; unsure means it could not
// decide. A CSS rule whose selector names abstain must therefore never paint with the warn palette.
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const dir = path.resolve(__dirname, "../app/styles");

describe("abstain styling", () => {
  const files = readdirSync(dir).filter((f) => f.endsWith(".css"));
  it.each(files)("%s never paints abstain with the warn palette", (file) => {
    const css = readFileSync(path.join(dir, file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    const rules = css.split("}").map((r) => r.split("{") as [string, string?]);
    for (const [selector, body] of rules) {
      if (body && /abstain/.test(selector)) expect(body, `${file}: ${selector.trim()}`).not.toMatch(/--warn/);
    }
  });
});
