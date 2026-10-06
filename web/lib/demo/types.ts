// The readiness document of the demo's control service (core-go internal/codespace Status), reduced to what the web needs: the
// cases and which one is active. Plain data: no URL, PID or environment ever appears in it.

export type DemoPhase = "ready" | "starting" | "busy" | "setup" | "needs-secrets" | "attention";

export interface DemoService {
  name: string;
  state: string;
  healthy: boolean;
}

export interface DemoCase {
  slot: string;
  label: string;
  seeded: boolean;
  active: boolean;
  manifest_id?: string;
  account_id?: string;
  opportunity_id?: string;
  /** Event-N-invisible status, read for the active case only: withheld (before Play), released (after), leaked, unknown. */
  invisibility?: string;
}

export interface DemoStatus {
  ready: boolean;
  phase: DemoPhase;
  message: string;
  services: DemoService[];
  cases: DemoCase[];
  active_case?: string;
  missing_secrets: string[];
  checked_at?: string;
}

/** What the web server knows about the control service: not a codespace, not up yet, or a status. */
export type StatusResult = { kind: "unconfigured" } | { kind: "unreachable" } | { kind: "ok"; status: DemoStatus };
