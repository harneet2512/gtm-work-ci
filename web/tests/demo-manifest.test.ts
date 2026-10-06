// The demo manifest is preselected from configuration (the core serves no manifest list), so nobody types a uuid.
import { describe, expect, it } from "vitest";
import { demoManifestFromEnv, manifestNotice, resolveManifest } from "@/lib/demo-manifest";

const ID = "0d3a0000-0000-4000-8000-000000000501";

describe("demoManifestFromEnv", () => {
  it("reads and lower-cases a configured uuid", () => {
    expect(demoManifestFromEnv({ GHOST_DEMO_MANIFEST_ID: ` ${ID.toUpperCase()} ` })).toBe(ID);
  });

  it("is null when unset, blank or not a uuid", () => {
    expect(demoManifestFromEnv({})).toBeNull();
    expect(demoManifestFromEnv({ GHOST_DEMO_MANIFEST_ID: "  " })).toBeNull();
    expect(demoManifestFromEnv({ GHOST_DEMO_MANIFEST_ID: "../etc" })).toBeNull();
  });
});

describe("resolveManifest", () => {
  const env = { GHOST_DEMO_MANIFEST_ID: ID };

  it("prefers the URL's manifest over the configured default", () => {
    const other = "0d3a0000-0000-4000-8000-000000000502";
    expect(resolveManifest(other, env)).toEqual({ manifestId: other, source: "url" });
  });

  it("falls back to the configured default and says so", () => {
    expect(resolveManifest(undefined, env)).toEqual({ manifestId: ID, source: "default" });
  });

  it("has no manifest when neither is usable", () => {
    expect(resolveManifest(undefined, {})).toEqual({ manifestId: null, source: "none" });
    expect(resolveManifest("not-a-uuid", {})).toEqual({ manifestId: null, source: "invalid" });
  });

  it("does not silently swap an invalid URL manifest for the default", () => {
    expect(resolveManifest("not-a-uuid", env)).toEqual({ manifestId: null, source: "invalid" });
  });
});

describe("configured manifest that is malformed", () => {
  it("is reported, not silently ignored", () => {
    expect(resolveManifest(undefined, { GHOST_DEMO_MANIFEST_ID: "../etc" })).toEqual({ manifestId: null, source: "invalid_config" });
  });
  it("a valid URL manifest still wins over a malformed configured one", () => {
    expect(resolveManifest(ID, { GHOST_DEMO_MANIFEST_ID: "nope" })).toEqual({ manifestId: ID, source: "url" });
  });
  it("blank configuration is simply none", () => {
    expect(resolveManifest(undefined, { GHOST_DEMO_MANIFEST_ID: "  " })).toEqual({ manifestId: null, source: "none" });
  });
});

describe("manifestNotice", () => {
  it("explains a bad URL id and a bad configured id differently, and is silent otherwise", () => {
    expect(manifestNotice("invalid")).toBe("That account id is not a valid id.");
    expect(manifestNotice("invalid_config")).toMatch(/configured/);
    expect(manifestNotice("none")).toBeNull();
    expect(manifestNotice("url")).toBeNull();
    expect(manifestNotice("default")).toBeNull();
  });
});
