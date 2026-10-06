import { describe, expect, it } from "vitest";
import { storeLabel, systemView } from "@/lib/view/system";
import type { InvisibilityReport, OutboxEvent, ProviderBreakerStatus } from "@/lib/api/types";

const closed: ProviderBreakerStatus = { state: "closed", consecutive_failures: 0, threshold: 3, cooldown_seconds: 30, trips_total: 0, rejected_total: 0, opened_at: null, last_reason: "" };
const open: ProviderBreakerStatus = { ...closed, state: "open", consecutive_failures: 3, rejected_total: 12, opened_at: "2026-10-04T05:00:00Z", last_reason: "provider credits exhausted" };
const withheld: InvisibilityReport = { manifest_id: "0d3a0000-0000-4000-0000-000000000501", held_out_event_id: "05e0ad00-0000-4000-8000-000000000301", status: "withheld", checked: ["postgres", "neo4j"], leaks: [] };
const event: OutboxEvent = { id: 41, topic: "strategy_set.published", account_id: "0a0cad00-0000-4000-8000-00000000ad01", created_at: "2026-10-04T05:10:00Z", agent_run_id: "0f0aad00-0000-4000-8000-00000000ad60", strategy_set_id: "0550ad00-0000-4000-8000-00000000ad55", decision_episode_id: "0e9ead00-0000-4000-8000-00000000ad0a" } as OutboxEvent;

describe("systemView (HAR-145)", () => {
  it("healthy fixture world: everything ok", () => {
    const v = systemView({ coreOk: true, breaker: closed, outbox: [], invisibility: withheld, invisibilityAsked: true });
    expect(v.core.status).toBe("ok");
    expect(v.breaker.status).toBe("ok");
    expect(v.breaker.state).toBe("closed");
    expect(v.queue.status).toBe("ok");
    expect(v.invisibility.status).toBe("ok");
    expect(v.invisibility.summary).toContain("Withheld");
    expect(v.invisibility.summary).toContain("2 stores");
  });

  it("open breaker is bad and says why", () => {
    const v = systemView({ coreOk: true, breaker: open, outbox: [], invisibility: null, invisibilityAsked: true });
    expect(v.breaker.status).toBe("bad");
    expect(v.breaker.summary).toContain("Open");
    expect(v.breaker.detail?.rejected).toBe(12);
  });

  it("unacknowledged outbox events warn and list", () => {
    const v = systemView({ coreOk: true, breaker: closed, outbox: [event], invisibility: null, invisibilityAsked: false });
    expect(v.queue.status).toBe("warn");
    expect(v.queue.events).toHaveLength(1);
    expect(v.invisibility.status).toBe("unknown");
    expect(v.invisibility.summary).toContain("Not checked");
  });

  it("unreadable pieces are unknown, never green; a leaked hold-out is bad", () => {
    const leaked: InvisibilityReport = { ...withheld, status: "leaked", leaks: [{ store: "postgres", kind: "activity", id: "act-1" }] };
    const v = systemView({ coreOk: false, breaker: null, outbox: null, invisibility: leaked, invisibilityAsked: true });
    expect(v.core.status).toBe("bad");
    expect(v.breaker.state).toBe("unreadable");
    expect(v.queue.status).toBe("unknown");
    expect(v.invisibility.status).toBe("bad");
    expect(v.invisibility.summary).toContain("LEAKED");
    expect(v.invisibility.report?.leaks[0]?.store).toBe("postgres");
  });
});

describe("storeLabel", () => {
  it("names the inspected stores without product names, and passes an unknown store through", () => {
    expect(storeLabel("postgres")).toBe("records");
    expect(storeLabel("neo4j")).toBe("graph");
    expect(storeLabel("cache")).toBe("cache");
  });
});

describe("system wording", () => {
  it("names no endpoint, health-check path or ticket", () => {
    const text = [
      systemView({ coreOk: true, breaker: closed, outbox: [], invisibility: withheld, invisibilityAsked: true }),
      systemView({ coreOk: false, breaker: null, outbox: null, invisibility: null, invisibilityAsked: true }),
      systemView({ coreOk: true, breaker: closed, outbox: [event], invisibility: null, invisibilityAsked: false }),
    ]
      .map((v) => [v.core.summary, v.breaker.summary, v.queue.summary, v.invisibility.summary].join(" "))
      .join(" ");
    expect(text).not.toMatch(/\/healthz|\/outbox|\/provider|HAR-|the core|outbox/i);
  });
});
