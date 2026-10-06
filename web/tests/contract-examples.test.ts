// HAR-98 acceptance "TS conformance tests", moved to WP4: every contracts/examples/*.example.json validates
// against its contracts/schemas/*.v1.json from TypeScript, the twin of the Go and Python conformance tests.
import { readdirSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { CONTRACTS_DIR, loadExample, validateSchema } from "./contract-validator";

const names = readdirSync(path.join(CONTRACTS_DIR, "examples"))
  .filter((f) => f.endsWith(".example.json"))
  .map((f) => f.slice(0, -".example.json".length))
  .sort();
const schemas = new Set(
  readdirSync(path.join(CONTRACTS_DIR, "schemas"))
    .filter((f) => f.endsWith(".v1.json"))
    .map((f) => f.slice(0, -".v1.json".length)),
);

describe("contract examples validate from TypeScript", () => {
  it("every example has a schema of the same name", () => {
    expect(names.filter((n) => !schemas.has(n))).toEqual([]);
  });

  it.each(names)("%s", (name) => {
    expect(validateSchema(name, loadExample(name)).errors).toEqual([]);
  });

  it("rejects an example with a required property removed", () => {
    const { id: _id, ...broken } = loadExample<Record<string, unknown>>("activity");
    expect(validateSchema("activity", broken).valid).toBe(false);
  });
});
