import type { DemoCase, DemoStatus } from "@/lib/demo/types";

export const MANIFEST_1 = "0d3a0000-0000-4000-8000-000000000501";
export const ACCOUNT_1 = "0d3a0000-0000-4000-8000-000000000101";
export const MANIFEST_2 = "0d3a0000-0000-4000-8000-000000000502";
export const ACCOUNT_2 = "0d3a0000-0000-4000-8000-000000000102";

export const SERVICES = ["neo4j", "postgres", "worker", "core", "slackbot", "web"].map((name) => ({ name, state: "running", healthy: true }));

export const CASE_1: DemoCase = {
  slot: "case1",
  label: "MedTech Advances",
  seeded: true,
  active: true,
  manifest_id: MANIFEST_1,
  account_id: ACCOUNT_1,
  opportunity_id: "006Wt000007BHzBIAW",
  invisibility: "withheld",
};

export const CASE_2: DemoCase = {
  slot: "case2",
  label: "EcoLite Innovations",
  seeded: true,
  active: false,
  manifest_id: MANIFEST_2,
  account_id: ACCOUNT_2,
  opportunity_id: "006Wt000007BDAnIAO",
};

/** A ready status as the control service serves it; override fields per test. */
export function statusDoc(over: Partial<DemoStatus> = {}): DemoStatus {
  return {
    ready: true,
    phase: "ready",
    message: "All systems ready",
    services: SERVICES,
    cases: [CASE_1, CASE_2],
    active_case: "case1",
    missing_secrets: [],
    checked_at: "2026-10-04T12:00:00Z",
    ...over,
  };
}

/** The wire form (what the Go service marshals): optional fields absent, not undefined. */
export const wire = (s: DemoStatus): Record<string, unknown> => JSON.parse(JSON.stringify(s)) as Record<string, unknown>;
