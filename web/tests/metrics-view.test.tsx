// @vitest-environment jsdom
// The episode's operational metrics (HAR-145): a METRIC, never an eval. A figure nobody reported reads "not measured",
// a replayed run reports no spend, a run with no recorded usage is "not measured" (not zero, not free), and the worker
// call time is labelled for what it is (a sum over calls, not elapsed time).
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Metrics } from "@/components/system/Metrics";
import type { OperationalMetrics } from "@/lib/api/types";
import { metricsView } from "@/lib/view/metrics";
import { loadExample } from "./contract-validator";

afterEach(cleanup);

const live = loadExample<OperationalMetrics>("operational_metrics");
const zeros = { model_calls: 0, input_tokens: 0, output_tokens: 0, cached_input_tokens: null, reasoning_tokens: null, tool_calls: 0, context_pulls: 0, retries: 0, cost_usd: null, models: [], stages: [], latency: { worker_call_ms: 0, model_ms: 0 } };
const replayed: OperationalMetrics = { ...live, ...zeros, measured: false, usage_source: "replay" };
const none: OperationalMetrics = { ...live, ...zeros, measured: false, usage_source: null };

const fact = (v: ReturnType<typeof metricsView>, label: string) => v.facts.find((f) => f.label === label)!.value;

describe("metricsView", () => {
  it("shows live figures as reported, with cost in dollars and counts grouped", () => {
    const v = metricsView(live);
    expect(v.status).toBe("measured from live model calls");
    expect(fact(v, "Model calls")).toBe("5");
    expect(fact(v, "Input tokens")).toBe("18,420");
    expect(fact(v, "Cached input tokens")).toBe("9,000");
    expect(fact(v, "Reasoning tokens")).toBe("640");
    expect(fact(v, "Cost")).toBe("$0.0123");
    expect(fact(v, "Retries")).toBe("1");
    expect(v.models).toEqual(["deepseek-v4-flash"]);
  });

  it("labels the worker call time for what it is, not as elapsed time", () => {
    const v = metricsView(live);
    const worker = v.facts.find((f) => f.label.startsWith("Worker call time"))!;
    expect(worker.label).toBe("Worker call time (summed over calls, not elapsed time)");
    expect(worker.value).toBe("21.4 s");
    expect(fact(v, "Model time (summed over calls)")).toBe("19.8 s");
  });

  it("reads a figure nobody reported as not measured, never as zero or as a free run", () => {
    const v = metricsView({ ...live, cached_input_tokens: null, reasoning_tokens: null, cost_usd: null });
    expect(fact(v, "Cached input tokens")).toBe("not measured");
    expect(fact(v, "Reasoning tokens")).toBe("not measured");
    expect(fact(v, "Cost")).toBe("not measured");
  });

  it("reports no spend for a replayed run and does not present its zeros as measurements", () => {
    const v = metricsView(replayed);
    expect(v.status).toBe("replayed from recorded answers: nothing was spent");
    expect(v.facts.every((f) => f.value === "not measured")).toBe(true);
    expect(v.facts.find((f) => f.label === "Cost")!.value).toBe("not measured");
    expect(v.note).toContain("no spend");
  });

  it("reads a run with no recorded usage as not measured", () => {
    const v = metricsView(none);
    expect(v.status).toBe("not measured");
    expect(v.facts.every((f) => f.value === "not measured")).toBe(true);
    expect(v.note).toContain("No model usage was recorded");
  });

  it("lists the stages in words, with their own call counts and not-measured cost", () => {
    const v = metricsView({ ...live, stages: [{ ...live.stages[0]!, cost_usd: null }, live.stages[1]!] });
    expect(v.stages.map((s) => s.name)).toEqual(["Strategies", "Judging"]);
    expect(v.stages[0]).toMatchObject({ modelCalls: "3", cost: "not measured", workerTime: "14.0 s" });
    expect(v.stages[1]!.cost).toBe("$0.0043");
  });

  it("formats times under a second and over a minute", () => {
    const v = metricsView({ ...live, latency: { worker_call_ms: 480, model_ms: 125_000 } });
    expect(fact(v, "Worker call time (summed over calls, not elapsed time)")).toBe("480 ms");
    expect(fact(v, "Model time (summed over calls)")).toBe("2 min 5 s");
  });
});

describe("Metrics", () => {
  it("is labelled a metric, never an eval, and has no verdict marks", () => {
    render(<Metrics state={{ state: "ok", metrics: live }} />);
    const panel = screen.getByRole("region", { name: "Operational metrics" });
    expect(within(panel).getAllByText(/metric/i).length).toBeGreaterThan(0);
    expect(panel.textContent).not.toMatch(/eval|✓|✗|pass|fail/i);
  });

  it("shows each fact and the per-stage table", () => {
    render(<Metrics state={{ state: "ok", metrics: live }} />);
    expect(screen.getByText("Input tokens").nextElementSibling!.textContent).toBe("18,420");
    expect(screen.getAllByRole("row").length).toBe(1 + 2);
  });

  it("says a replayed run spent nothing, and shows nothing as a measurement", () => {
    render(<Metrics state={{ state: "ok", metrics: replayed }} />);
    expect(screen.getByText("replayed from recorded answers: nothing was spent")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it.each([
    [{ state: "none_asked" }, /Open a demo account to read its metrics/],
    [{ state: "no_episode" }, /No decision episode has been recorded yet/],
    [{ state: "not_found" }, /No metrics are recorded for that episode/],
    [{ state: "unavailable" }, /Backend unavailable: the metrics could not be read/],
  ] as const)("explains %j in words", (state, text) => {
    render(<Metrics state={state} />);
    expect(screen.getByRole("region", { name: "Operational metrics" }).textContent).toMatch(text);
  });
});
