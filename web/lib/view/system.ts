// The System panel's view model (HAR-145): every status derives from a real core answer — liveness,
// the provider breaker (HAR-135), the surface outbox queue (HAR-117) and the held-out event's leak
// check (HAR-129 §B). A failed fetch is "unreadable", never silently green.

import type { InvisibilityReport, OutboxEvent, ProviderBreakerStatus } from "../api/types";

/** The stores the leak check inspects, in the audience's words (the report names them by product). */
const STORE_WORD: Record<string, string> = { postgres: "records", neo4j: "graph" };
export const storeLabel = (store: string): string => STORE_WORD[store] ?? store;

export type BandStatus = "ok" | "warn" | "bad" | "unknown";

export interface SystemView {
  core: { status: BandStatus; summary: string };
  breaker: {
    status: BandStatus;
    state: ProviderBreakerStatus["state"] | "unreadable";
    summary: string;
    detail: { trips: number; rejected: number; threshold: number; consecutive: number; openedAt: string | null; lastReason: string } | null;
  };
  queue: {
    status: BandStatus;
    /** null = the outbox endpoint could not be read; [] = the consumer has nothing unacknowledged. */
    events: OutboxEvent[] | null;
    summary: string;
  };
  /** No manifest → not asked; the section explains the leak check exists but wasn't run. */
  invisibility: {
    status: BandStatus;
    report: InvisibilityReport | null;
    asked: boolean;
    summary: string;
  };
}

const breakerSummary = (b: ProviderBreakerStatus): { status: BandStatus; summary: string } => {
  if (b.state === "open")
    return { status: "bad", summary: `Open — every model call refused locally (${b.rejected_total} rejected since)` };
  if (b.state === "half_open")
    return { status: "warn", summary: `Half-open — one probe call allowed; ${b.consecutive_failures}/${b.threshold} failures counted` };
  if (b.trips_total > 0) return { status: "warn", summary: `Closed, but tripped ${b.trips_total}× since start` };
  return { status: "ok", summary: `Closed — no provider failures counted (threshold ${b.threshold})` };
};

const queueSummary = (events: OutboxEvent[] | null): { status: BandStatus; summary: string } => {
  if (events === null) return { status: "unknown", summary: "The delivery queue could not be read: state unknown" };
  if (events.length === 0) return { status: "ok", summary: "Drained: nothing is waiting for delivery" };
  return { status: "warn", summary: `${events.length} event${events.length === 1 ? "" : "s"} waiting for delivery to Cliff` };
};

const invisibilitySummary = (report: InvisibilityReport | null, asked: boolean): { status: BandStatus; summary: string } => {
  if (!asked) return { status: "unknown", summary: "Not checked: open the page on a demo account to run the leak check" };
  if (report === null) return { status: "unknown", summary: "Not observable: no report is available for that demo account" };
  if (report.status === "leaked") return { status: "bad", summary: `LEAKED — ${report.leaks.length} escape${report.leaks.length === 1 ? "" : "s"} found while withheld` };
  if (report.status === "released") return { status: "ok", summary: "Released — the event is in the world now; withholding no longer applies" };
  return { status: "ok", summary: `Withheld — ${report.checked.length} store${report.checked.length === 1 ? "" : "s"} inspected, nothing leaked` };
};

export function systemView(input: {
  coreOk: boolean;
  breaker: ProviderBreakerStatus | null;
  outbox: OutboxEvent[] | null;
  invisibility: InvisibilityReport | null;
  invisibilityAsked: boolean;
}): SystemView {
  const br = input.breaker;
  const bs = br ? breakerSummary(br) : { status: "unknown" as const, summary: "Breaker state not observable" };
  const q = queueSummary(input.outbox);
  const inv = invisibilitySummary(input.invisibility, input.invisibilityAsked);
  return {
    core: input.coreOk
      ? { status: "ok", summary: "The backend answers its health check" }
      : { status: "bad", summary: "The backend is unreachable or unhealthy: everything below may be stale" },
    breaker: {
      status: bs.status,
      state: br?.state ?? "unreadable",
      summary: bs.summary,
      detail: br
        ? { trips: br.trips_total, rejected: br.rejected_total, threshold: br.threshold, consecutive: br.consecutive_failures, openedAt: br.opened_at, lastReason: br.last_reason }
        : null,
    },
    queue: { status: q.status, events: input.outbox, summary: q.summary },
    invisibility: { status: inv.status, report: input.invisibility, asked: input.invisibilityAsked, summary: inv.summary },
  };
}
