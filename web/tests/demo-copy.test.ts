// Audience-facing copy never names developer tooling (product-owner rule for the demo surface).
import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const files = (dir: string): string[] =>
  readdirSync(path.join(root, dir)).flatMap((f) => {
    const rel = `${dir}/${f}`;
    if (statSync(path.join(root, rel)).isDirectory()) return files(rel);
    return rel.endsWith(".tsx") ? [rel] : [];
  });

const BANNED = /ghostctl|\bGHOST_[A-Z_]{3,}|\bnpm run\b|\bnpx\b|go test|pytest|playwright/;

describe("demo copy", () => {
  it.each([...files("app"), ...files("components")])("%s names no developer tooling in rendered text", (rel) => {
    const text = readFileSync(path.join(root, rel), "utf8")
      .split("\n")
      .filter((l) => !/^\s*(\/\/|\*|\/\*)/.test(l) && !/process\.env/.test(l))
      .join("\n");
    expect(text).not.toMatch(BANNED);
  });
});
