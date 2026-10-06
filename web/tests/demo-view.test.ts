import { describe, expect, it } from "vitest";
import { handoffAvailable, landingTarget } from "@/lib/demo/view";
import { CASE_1, CASE_2, MANIFEST_1, MANIFEST_2, statusDoc } from "./demo-fixtures";

describe("landingTarget", () => {
  it("opens the control plane on the active case's frozen replay, in demo mode", () => {
    expect(landingTarget({ kind: "ok", status: statusDoc() })).toBe(`/control?manifest=${MANIFEST_1}&demo=1`);
  });

  it("follows the active case when the operator continued with the later one", () => {
    const status = statusDoc({ cases: [{ ...CASE_1, active: false }, { ...CASE_2, active: true }] });
    expect(landingTarget({ kind: "ok", status })).toBe(`/control?manifest=${MANIFEST_2}&demo=1`);
  });

  it("has no target without an active, frozen case or without the demo service", () => {
    expect(landingTarget({ kind: "unconfigured" })).toBeNull();
    expect(landingTarget({ kind: "unreachable" })).toBeNull();
    expect(landingTarget({ kind: "ok", status: statusDoc({ cases: [{ ...CASE_1, seeded: false }] }) })).toBeNull();
    expect(landingTarget({ kind: "ok", status: statusDoc({ cases: [{ ...CASE_1, manifest_id: undefined }] }) })).toBeNull();
    expect(landingTarget({ kind: "ok", status: statusDoc({ cases: [{ ...CASE_1, active: false }] }) })).toBeNull();
  });

  it("never points at an admin page", () => {
    expect(landingTarget({ kind: "ok", status: statusDoc() })).not.toContain("admin");
  });
});

describe("handoffAvailable", () => {
  const status = (over: Parameters<typeof statusDoc>[0] = {}) => ({ kind: "ok" as const, status: statusDoc(over) });

  it("is true when the manifest's case has a later, frozen case after it", () => {
    expect(handoffAvailable(status(), MANIFEST_1)).toBe(true);
  });

  it("is false on the last case, for an unknown manifest and while the later case is not frozen", () => {
    expect(handoffAvailable(status(), MANIFEST_2)).toBe(false);
    expect(handoffAvailable(status(), "0d3a0000-0000-4000-8000-0000000009ff")).toBe(false);
    expect(handoffAvailable(status({ cases: [CASE_1, { ...CASE_2, seeded: false }] }), MANIFEST_1)).toBe(false);
  });

  it("is false outside the laptop demo or while the control service is unreachable", () => {
    expect(handoffAvailable({ kind: "unconfigured" }, MANIFEST_1)).toBe(false);
    expect(handoffAvailable({ kind: "unreachable" }, MANIFEST_1)).toBe(false);
  });
});
