// A NUL byte in a stylesheet makes git treat it as binary and silently breaks the rule that contained it.
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const dir = path.resolve(__dirname, "../app/styles");
const files = readdirSync(dir).filter((f) => f.endsWith(".css"));

describe("stylesheets", () => {
  it("finds the stylesheets", () => {
    expect(files.length).toBeGreaterThan(5);
  });
  it.each(files)("%s has no NUL byte", (f) => {
    expect(readFileSync(path.join(dir, f)).includes(0)).toBe(false);
  });
});
