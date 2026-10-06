// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AskTraceView, costText, durationText } from "@/components/ask/AskTraceView";
import { createCoreClient, InvalidIdError } from "@/lib/api/core-client";
import type { AskTrace } from "@/lib/api/types";

afterEach(cleanup);

const ID = "tr_0123456789abcdef01234567";

const trace: AskTrace = {
  id: ID,
  thread_ref: "C1:100.1",
  question: "Which evals warned on M2?",
  answer_markdown: "D4 warned: the email names no date.",
  created_at: "2026-10-06T12:00:00Z",
  model: "qwen/qwen3.8-flash",
  tokens_in: 1200,
  tokens_out: 300,
  cost_usd: 0.0123,
  duration_ms: 4200,
  timed_out: false,
  steps: [
    { n: 1, tool: "gate_results", args: { account: "MedTech" }, ok: true, empty: false, output: { items: [{ gate: "D4", verdict: "warn" }] }, links: [{ label: "Gate D4 in the trace", url: "http://web.test/episodes/e1" }] },
    { n: 2, tool: "draft_followup", args: { account: "MedTech" }, ok: true, empty: true, dry_run: true, output: {} },
  ],
};

describe("AskTraceView", () => {
  it("shows the question, the answer, every tool with its input and output, tokens and cost", () => {
    render(<AskTraceView trace={trace} />);
    expect(screen.getByText("Which evals warned on M2?")).toBeTruthy();
    expect(screen.getByText("D4 warned: the email names no date.")).toBeTruthy();
    expect(screen.getByText("Tools called (2)")).toBeTruthy();
    expect(screen.getByText("gate_results").tagName).toBe("CODE");
    expect(document.body.textContent).toContain('"verdict": "warn"');
    expect(document.body.textContent).toContain("1200 in, 300 out");
    expect(document.body.textContent).toContain("$0.0123");
    expect(screen.getByRole("link", { name: "Gate D4 in the trace" }).getAttribute("href")).toBe("http://web.test/episodes/e1");
  });

  it("labels a dry run and an empty result, and says so when no cost was reported", () => {
    render(<AskTraceView trace={{ ...trace, cost_usd: null, timed_out: true }} />);
    expect(document.body.textContent).toContain("dry run: nothing was sent or written");
    expect(document.body.textContent).toContain("Found nothing.");
    expect(document.body.textContent).toContain("not reported");
    expect(document.body.textContent).toContain("ran out of time");
  });

  it("says plainly when Cliff read nothing", () => {
    render(<AskTraceView trace={{ ...trace, steps: [] }} />);
    expect(screen.getByText("Cliff answered without reading anything.")).toBeTruthy();
  });

  it("formats cost and time", () => {
    expect([costText(null), costText(0.5), durationText(1234)]).toEqual(["not reported", "$0.5000", "1.2 s"]);
  });
});

describe("getAskTrace", () => {
  function api(handler: () => Response) {
    const calls: URL[] = [];
    const fetchImpl = vi.fn(async (input: string | URL | Request) => {
      calls.push(new URL(String(input)));
      return handler();
    }) as unknown as typeof fetch;
    return { client: createCoreClient({ baseUrl: "http://core.test:8080", token: "t0k", fetchImpl }), calls };
  }

  it("GETs /ask/traces/{id} and returns the trace", async () => {
    const { client, calls } = api(() => new Response(JSON.stringify(trace), { status: 200 }));
    await expect(client.getAskTrace(ID)).resolves.toEqual(trace);
    expect(calls[0]!.pathname).toBe(`/ask/traces/${ID}`);
  });

  it("propagates a 404 and refuses a malformed id before any request", async () => {
    const missing = api(() => new Response(JSON.stringify({ error: { code: "not_found", message: "none" } }), { status: 404 }));
    await expect(missing.client.getAskTrace(ID)).rejects.toMatchObject({ status: 404 });
    const { client, calls } = api(() => new Response("{}"));
    await expect(client.getAskTrace("../accounts")).rejects.toBeInstanceOf(InvalidIdError);
    expect(calls).toHaveLength(0);
  });
});
