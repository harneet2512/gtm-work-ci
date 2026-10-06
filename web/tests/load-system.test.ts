// The /system loader (HAR-145): independent reads, each failing on its own; metrics come from the latest decision episode.
import { describe, expect, it, vi } from "vitest";
import { CoreError } from "@/lib/api/core-client";
import { loadSystemPage, type SystemApi } from "@/lib/load-system";
import type { EpisodeReplayView, InvisibilityReport, OperationalMetrics, ProviderBreakerStatus } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const metrics = loadExample<OperationalMetrics>("operational_metrics");
const replay = loadFixture<EpisodeReplayView>("replay.episodes.json");
const withDecision = { ...replay, prior_episodes: replay.prior_episodes.map((e, i, all) => (i === all.length - 1 ? { ...e, decision_episode_id: "0de50000-0000-4000-8000-000000000202" } : { ...e, decision_episode_id: null })) } as EpisodeReplayView;
const breaker = { state: "closed", consecutive_failures: 0, threshold: 3, cooldown_seconds: 30, trips_total: 0, rejected_total: 0, opened_at: null, last_reason: "" } as ProviderBreakerStatus;
const report = { manifest_id: MANIFEST, held_out_event_id: "05e0ad00-0000-4000-8000-000000000301", status: "withheld", checked: ["postgres"], leaks: [] } as InvisibilityReport;

const boom = async (): Promise<never> => {
  throw new CoreError(500, "internal", "x");
};

function fakeApi(over: Partial<SystemApi> = {}): SystemApi {
  return {
    getHealth: vi.fn(async () => ({ status: "ok" })),
    getProviderBreaker: vi.fn(async () => breaker),
    listOutboxEvents: vi.fn(async () => []),
    getInvisibility: vi.fn(async () => report),
    getReplayEpisodes: vi.fn(async () => withDecision),
    getEpisodeMetrics: vi.fn(async () => metrics),
    ...over,
  };
}

describe("loadSystemPage", () => {
  it("reads health, breaker, delivery queue, the leak check and the latest decision episode's metrics", async () => {
    const api = fakeApi();
    const data = await loadSystemPage(api, MANIFEST);
    expect(data).toEqual({ coreOk: true, breaker, outbox: [], invisibility: report, invisibilityAsked: true, metrics: { state: "ok", metrics } });
    expect(api.getEpisodeMetrics).toHaveBeenCalledWith("0de50000-0000-4000-8000-000000000202");
    expect(api.listOutboxEvents).toHaveBeenCalledWith("slack");
  });

  it("asks no manifest-scoped question without a manifest", async () => {
    const api = fakeApi();
    const data = await loadSystemPage(api, null);
    expect(data).toMatchObject({ invisibility: null, invisibilityAsked: false, metrics: { state: "none_asked" } });
    expect(api.getInvisibility).not.toHaveBeenCalled();
    expect(api.getReplayEpisodes).not.toHaveBeenCalled();
  });

  it("fails each read on its own: unreadable pieces are null/false, never green", async () => {
    const data = await loadSystemPage(fakeApi({ getHealth: boom, getProviderBreaker: boom, listOutboxEvents: boom, getInvisibility: boom }), MANIFEST);
    expect(data).toMatchObject({ coreOk: false, breaker: null, outbox: null, invisibility: null, invisibilityAsked: true });
    expect(data.metrics.state).toBe("ok");
  });

  it("reads an unhealthy answer as not ok", async () => {
    expect((await loadSystemPage(fakeApi({ getHealth: vi.fn(async () => ({ status: "degraded" })) }), null)).coreOk).toBe(false);
  });

  it("says there are no metrics yet when no episode opened a decision", async () => {
    const none = { ...replay, prior_episodes: replay.prior_episodes.map((e) => ({ ...e, decision_episode_id: null })) } as EpisodeReplayView;
    const data = await loadSystemPage(fakeApi({ getReplayEpisodes: vi.fn(async () => none) }), MANIFEST);
    expect(data.metrics).toEqual({ state: "no_episode" });
  });

  it("keeps an unknown episode (404) apart from an unreachable backend", async () => {
    expect((await loadSystemPage(fakeApi({ getEpisodeMetrics: vi.fn(async () => null) }), MANIFEST)).metrics).toEqual({ state: "not_found" });
    expect((await loadSystemPage(fakeApi({ getEpisodeMetrics: boom }), MANIFEST)).metrics).toEqual({ state: "unavailable" });
    expect((await loadSystemPage(fakeApi({ getReplayEpisodes: boom }), MANIFEST)).metrics).toEqual({ state: "unavailable" });
  });
});
