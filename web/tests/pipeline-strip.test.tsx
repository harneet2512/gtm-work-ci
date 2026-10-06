// @vitest-environment jsdom
// The Play strip (HAR-145): Play posts the release while the strip polls the real progress read, on real timers (a short
// poll period), until the pipeline is complete or failed. Nothing lights from a timer; "passed" belongs to the evals
// stage; a transport problem reads "backend unavailable".
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AdvanceResult, PipelineProgress, PipelineStageRow } from "@/lib/api/types";
import type { ProgressRead } from "@/lib/progress-client";
import { STAGE_IDS } from "@/lib/view/pipeline";

const nav = vi.hoisted(() => ({ refresh: vi.fn(), push: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: nav.refresh, push: nav.push }) }));

const { PipelineStrip } = await import("@/components/control/PipelineStrip");

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const POLL = 10;
const nextEvent = { position: 3, occurredAt: "2026-10-05T09:00:00Z", source: "email", provenance: "crmarena-pro:b2b" };

const row = (stage: PipelineStageRow["stage"], over: Partial<PipelineStageRow> = {}): PipelineStageRow => ({
  stage,
  status: "waiting",
  attempt: 0,
  started_at: null,
  ended_at: null,
  duration_ms: null,
  refs: {},
  eval_result_ids: [],
  failure_kind: null,
  detail: null,
  seq: null,
  updated_at: null,
  ...over,
});

const progress = (overall: PipelineProgress["overall"], rows: Partial<Record<PipelineStageRow["stage"], Partial<PipelineStageRow>>> = {}): ProgressRead => ({
  ok: true,
  progress: { scope: "manifest", manifest_id: MANIFEST, run_id: null, account_id: "0a0c0000-0000-4000-8000-000000000001", overall, stages: STAGE_IDS.map((id) => row(id, rows[id])), generated_at: "2026-10-05T10:00:00Z" },
});

const done = (id: PipelineStageRow["stage"], status: PipelineStageRow["status"] = "completed") => ({ status, ended_at: "2026-10-05T10:00:01Z", duration_ms: 900 }) satisfies Partial<PipelineStageRow> & { status: typeof status };
const allDone = () => ({ ingest: done("ingest"), resolve: done("resolve"), graph: done("graph"), state: done("state"), decide: done("decide"), evals: done("evals", "passed"), cliff: done("cliff") });

const deferred = () => {
  let open!: () => void;
  const promise = new Promise<void>((resolve) => (open = resolve));
  return { promise, open };
};

const advance = (over: Partial<AdvanceResult> = {}): AdvanceResult => ({ manifest_id: MANIFEST, episode: 3, total: 4, material: true, no_action_reason: null, ...over }) as AdvanceResult;
const played = (over: Partial<AdvanceResult> = {}) => vi.fn(async () => ({ ok: true as const, result: advance(over) }));
const stageText = (label: string) => [...document.querySelectorAll(".stage")].find((li) => li.querySelector(".stage-label")!.textContent === label)!.textContent ?? "";

const mount = (props: Partial<React.ComponentProps<typeof PipelineStrip>> & Pick<React.ComponentProps<typeof PipelineStrip>, "play">) =>
  render(<PipelineStrip manifestId={MANIFEST} canPlayNext nextEvent={nextEvent} pollMs={POLL} {...props} />);
const press = () => fireEvent.click(screen.getByRole("button", { name: "Play event N" }));

beforeEach(() => {
  nav.refresh.mockClear();
  nav.push.mockClear();
});
afterEach(cleanup);

describe("before Play", () => {
  it("starts with seven waiting stages and describes the withheld event without its content", () => {
    mount({ play: vi.fn() });
    expect(document.querySelectorAll(".stage.st-waiting")).toHaveLength(7);
    expect(screen.getByRole("heading").textContent).toBe("Play event 3");
    expect(document.body.textContent).toContain("Held out: email · 2026-10-05T09:00:00Z · crmarena-pro:b2b");
    expect(document.body.textContent).toContain("withheld until release");
  });

  it("shows what the pipeline already did when the page loaded after a Play", () => {
    const read = progress("complete", allDone());
    mount({ play: vi.fn(), initial: read.ok ? read.progress : null });
    expect(stageText("Ingest")).toContain("completed");
    expect(stageText("Evals")).toContain("passed");
    expect(screen.getByText("Pipeline complete")).toBeTruthy();
  });

  it("disables Play and says everything is released when no event is left", () => {
    mount({ play: vi.fn(), canPlayNext: false, nextEvent: null });
    expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("heading").textContent).toBe("Play event N");
    expect(document.body.textContent).toContain("Every event in the manifest is released.");
  });
});

describe("Play and polling", () => {
  it("polls the progress read until the pipeline is complete, then refreshes the page once", async () => {
    const gate = deferred();
    const fetchProgress = vi
      .fn<(id: string) => Promise<ProgressRead>>()
      .mockResolvedValueOnce(progress("running", { ingest: done("ingest"), resolve: { status: "running", started_at: "2026-10-05T10:00:00Z" } }))
      .mockResolvedValueOnce(progress("running", { ingest: done("ingest"), resolve: done("resolve"), graph: done("graph"), state: done("state"), decide: { status: "running", started_at: "2026-10-05T10:00:02Z" } }))
      .mockImplementation(async () => {
        await gate.promise;
        return progress("complete", allDone());
      });
    mount({ play: played(), fetchProgress });
    press();

    await waitFor(() => expect(stageText("Decide")).toContain("running"));
    expect(nav.refresh).not.toHaveBeenCalled();
    gate.open();
    await waitFor(() => expect(screen.getByText("Pipeline complete")).toBeTruthy());
    expect(fetchProgress).toHaveBeenCalledWith(MANIFEST);
    expect(fetchProgress.mock.calls.length).toBeGreaterThanOrEqual(3);
    await waitFor(() => expect(nav.refresh).toHaveBeenCalledTimes(1));
  });

  it("shows finished stages as completed and the check mark only on the evals verdict", async () => {
    mount({ play: played(), fetchProgress: vi.fn(async () => progress("complete", allDone())) });
    press();
    await waitFor(() => expect(nav.refresh).toHaveBeenCalled());
    for (const label of ["Ingest", "Resolve", "Graph", "State", "Decide", "Cliff"]) {
      expect(stageText(label)).toContain("completed");
      expect(stageText(label)).not.toContain("✓");
      expect(stageText(label)).not.toContain("passed");
    }
    expect(stageText("Evals")).toContain("✓");
    expect(stageText("Evals")).toContain("passed");
  });

  it("does not stop on a final state it read while the Play request was still open", async () => {
    let finishPlay: (v: { ok: true; result: AdvanceResult }) => void = () => undefined;
    const play = vi.fn(() => new Promise<{ ok: true; result: AdvanceResult }>((resolve) => (finishPlay = resolve)));
    const fetchProgress = vi.fn(async () => progress("complete", allDone()));
    mount({ play, fetchProgress });
    press();
    await waitFor(() => expect(fetchProgress.mock.calls.length).toBeGreaterThanOrEqual(3));
    expect(nav.refresh).not.toHaveBeenCalled();
    finishPlay({ ok: true, result: advance() });
    await waitFor(() => expect(nav.refresh).toHaveBeenCalledTimes(1));
  });

  it("follows a failed pipeline to its end and names the failure kind", async () => {
    const failed = progress("failed", { ingest: done("ingest"), resolve: done("resolve"), graph: { status: "failed", ended_at: "2026-10-05T10:00:01Z", failure_kind: "internal", detail: "projection write rolled back" } });
    mount({ play: played(), fetchProgress: vi.fn(async () => failed) });
    press();
    await waitFor(() => expect(screen.getByText("Pipeline failed (internal)")).toBeTruthy());
    expect(stageText("Graph")).toContain("failed");
    expect(stageText("Graph")).toContain("failure: internal");
    expect(stageText("Graph")).toContain("projection write rolled back");
    await waitFor(() => expect(nav.refresh).toHaveBeenCalledTimes(1));
  });

  it("reads a transport failure as backend unavailable, not as an eval failure", async () => {
    const down = progress("failed", { decide: { status: "failed", ended_at: "2026-10-05T10:00:01Z", failure_kind: "transport", detail: "no heartbeat" } });
    mount({ play: played(), fetchProgress: vi.fn(async () => down) });
    press();
    await waitFor(() => expect(screen.getByText("Backend unavailable (transport)")).toBeTruthy());
    expect(stageText("Decide")).toContain("backend unavailable");
    expect(stageText("Decide")).toContain("failure: transport");
    expect(document.querySelectorAll(".stage.st-failed")).toHaveLength(0);
  });

  it("does not call a failing progress read an outage while Play is still open (the handoff restarts core), but does once Play has returned", async () => {
    const playGate = deferred();
    const done2 = deferred();
    const fetchProgress = vi
      .fn<(id: string) => Promise<ProgressRead>>()
      .mockResolvedValueOnce({ ok: false, reason: "unavailable" })
      .mockResolvedValueOnce({ ok: false, reason: "unavailable" })
      .mockResolvedValueOnce({ ok: false, reason: "unavailable" })
      .mockImplementation(async () => {
        await done2.promise;
        return progress("complete", allDone());
      });
    const play = vi.fn(async () => {
      await playGate.promise;
      return { ok: true as const, result: advance() };
    });
    mount({ play, fetchProgress });
    press();
    await waitFor(() => expect(fetchProgress.mock.calls.length).toBeGreaterThanOrEqual(3));
    expect(document.body.textContent).not.toMatch(/unavailable/i);
    expect(stageText("Ingest")).toContain("waiting");
    playGate.open();
    await waitFor(() => expect(fetchProgress.mock.calls.length).toBeGreaterThanOrEqual(4));
    done2.open();
    await waitFor(() => expect(screen.getByText("Pipeline complete")).toBeTruthy());
  });

  it("keeps polling through a progress outage after Play returned and recovers", async () => {
    const gate = deferred();
    const fetchProgress = vi
      .fn<(id: string) => Promise<ProgressRead>>()
      .mockImplementationOnce(async () => {
        await new Promise((r) => setTimeout(r, 30)); // the first read lands after Play has returned
        return { ok: false, reason: "unavailable" };
      })
      .mockImplementation(async () => {
        await gate.promise;
        return progress("complete", allDone());
      });
    mount({ play: played(), fetchProgress });
    press();
    await waitFor(() => expect(screen.getByText(/progress could not be read/)).toBeTruthy());
    expect(stageText("Decide")).toContain("backend unavailable");
    gate.open();
    await waitFor(() => expect(screen.getByText("Pipeline complete")).toBeTruthy());
    expect(stageText("Decide")).toContain("completed");
  });

  it("says a non-material event needs no action, with its reason when given", async () => {
    mount({ play: played({ material: false, no_action_reason: "duplicate" }), fetchProgress: vi.fn(async () => progress("complete", { ingest: done("ingest"), cliff: { status: "skipped", ended_at: "2026-10-05T10:00:01Z" } })) });
    press();
    await waitFor(() => expect(screen.getByText("Event 3 of 4: no action required (duplicate).")).toBeTruthy());
    expect(stageText("Cliff")).toContain("skipped");
  });

  it("omits the parenthetical for a non-material event with no stated reason", async () => {
    mount({ play: played({ material: false }), fetchProgress: vi.fn(async () => progress("complete")) });
    press();
    await waitFor(() => expect(screen.getByText("Event 3 of 4: no action required.")).toBeTruthy());
  });

  it("stops refreshing and says so when the pipeline never finishes", async () => {
    mount({ play: played(), fetchProgress: vi.fn(async () => progress("running", { ingest: done("ingest") })), maxPolls: 3 });
    press();
    await waitFor(() => expect(screen.getByText(/stopped refreshing/)).toBeTruthy());
    expect(nav.refresh).not.toHaveBeenCalled();
  });

  it("stops polling when unmounted", async () => {
    const fetchProgress = vi.fn(async () => progress("running"));
    const { unmount } = mount({ play: played(), fetchProgress });
    press();
    await waitFor(() => expect(fetchProgress).toHaveBeenCalled());
    unmount();
    const calls = fetchProgress.mock.calls.length;
    await new Promise((r) => setTimeout(r, POLL * 6));
    expect(fetchProgress.mock.calls.length).toBeLessThanOrEqual(calls + 1);
  });
});

describe("when the Play action itself rejects", () => {
  it("settles, stops polling and reads backend unavailable, never an eval failure", async () => {
    const fetchProgress = vi.fn(async () => progress("running", { ingest: done("ingest") }));
    const play = vi.fn(async () => {
      throw new Error("Failed to fetch");
    });
    mount({ play, fetchProgress, maxPolls: 2 });
    press();
    await waitFor(() => expect(screen.getByText(/Backend unavailable: Play was not delivered/)).toBeTruthy());
    await waitFor(() => expect((screen.getByRole("button") as HTMLButtonElement).textContent).toBe("Play event N"));
    const calls = fetchProgress.mock.calls.length;
    await new Promise((r) => setTimeout(r, POLL * 6));
    expect(fetchProgress.mock.calls.length).toBe(calls);
    expect(document.querySelectorAll(".stage.st-failed")).toHaveLength(0);
    expect(document.body.textContent).not.toMatch(/FAIL/);
    expect(document.querySelector(".pipeline-line.error")).toBeNull();
  });

  it("lets the operator press Play again afterwards", async () => {
    const play = vi.fn().mockRejectedValueOnce(new Error("down")).mockResolvedValue({ ok: true, result: advance() });
    mount({ play, fetchProgress: vi.fn(async () => progress("complete", allDone())) });
    press();
    await waitFor(() => expect(screen.getByText(/Play was not delivered/)).toBeTruthy());
    press();
    await waitFor(() => expect(nav.refresh).toHaveBeenCalledTimes(1));
  });
});

describe("the four kinds of problem stay distinct", () => {
  const evalsAs = (over: Partial<PipelineStageRow>) => progress("failed", { ingest: done("ingest"), evals: { ended_at: "2026-10-05T10:00:01Z", ...over } });
  const problemOf = (label: string) => document.querySelector(".stage .stage-problem")?.textContent ?? "";

  it("backend unavailable: a transport failure", async () => {
    mount({ play: played(), initial: (evalsAs({ status: "unknown", failure_kind: "transport" }) as { progress: PipelineProgress }).progress });
    expect(problemOf("Evals")).toBe("Backend unavailable");
  });
  it("eval failed to run: the judging stage could not judge, for a reason that is not transport", () => {
    mount({ play: vi.fn(), initial: (evalsAs({ status: "unknown", failure_kind: "internal" }) as { progress: PipelineProgress }).progress });
    expect(problemOf("Evals")).toBe("Eval failed to run");
  });
  it("trace incomplete: a stage whose outcome was never recorded", () => {
    mount({ play: vi.fn(), initial: (progress("running", { graph: { status: "unknown" } }) as { progress: PipelineProgress }).progress });
    expect(problemOf("Graph")).toBe("Trace incomplete");
  });
  it("eval FAIL: only a judged failure on the evals stage", () => {
    mount({ play: vi.fn(), initial: (evalsAs({ status: "failed", failure_kind: null, eval_result_ids: ["x"] }) as { progress: PipelineProgress }).progress });
    expect(problemOf("Evals")).toBe("Eval FAIL");
  });
  it("a healthy stage carries no problem label", () => {
    mount({ play: vi.fn(), initial: (progress("complete", allDone()) as { progress: PipelineProgress }).progress });
    expect(document.querySelectorAll(".stage-problem")).toHaveLength(0);
  });
});

describe("when Play does not release the event", () => {
  it("shows the core's refusal in plain words and one last read of the real progress", async () => {
    const fetchProgress = vi.fn(async () => progress("not_started"));
    mount({ play: vi.fn(async () => ({ ok: false as const, code: "already_released" })), fetchProgress });
    press();
    await waitFor(() => expect(screen.getByText("Play has already completed for this event. Nothing changes.")).toBeTruthy());
    expect(screen.getByText("Play has already completed for this event. Nothing changes.").className).toContain("error");
    await waitFor(() => expect(screen.getByText("Not started")).toBeTruthy());
    expect(nav.refresh).not.toHaveBeenCalled();
  });

  it("falls back to the code for a refusal it has no words for", async () => {
    mount({ play: vi.fn(async () => ({ ok: false as const, code: "odd_code" })), fetchProgress: vi.fn(async () => progress("not_started")) });
    press();
    await waitFor(() => expect(screen.getByText("Play was refused (odd_code).")).toBeTruthy());
  });

  it("reads an unreachable core as backend unavailable in a neutral line", async () => {
    mount({ play: vi.fn(async () => ({ ok: false as const, code: "unreachable" })), fetchProgress: vi.fn(async () => ({ ok: false as const, reason: "unavailable" as const })) });
    press();
    const line = await screen.findByText("Backend unavailable: Play was not delivered, so nothing was released.");
    expect(line.className).not.toContain("error");
    expect(document.querySelectorAll(".stage.st-failed")).toHaveLength(0);
  });
});

describe("when Play continues into the next account", () => {
  const SECOND = "0d3a0000-0000-4000-8000-000000000502";

  it("keeps Play for the next episode when this account has none left, and does not say everything is released", () => {
    mount({ play: vi.fn(), nextEvent: null });
    expect((screen.getByRole("button", { name: "Play event N" }) as HTMLButtonElement).disabled).toBe(false);
    expect(document.body.textContent).toContain("The next episode is withheld until release.");
    expect(document.body.textContent).not.toContain("Every event in the manifest is released.");
  });

  it("follows the next account (the result names its manifest) and opens it once the pipeline is complete", async () => {
    const fetchProgress = vi.fn<(id: string) => Promise<ProgressRead>>().mockResolvedValue(progress("complete", allDone()));
    mount({ play: played({ manifest_id: SECOND }), fetchProgress, nextEvent: null });
    press();
    await waitFor(() => expect(nav.push).toHaveBeenCalledTimes(1));
    expect(nav.push).toHaveBeenCalledWith(`/control?manifest=${SECOND}&demo=1`);
    expect(fetchProgress).toHaveBeenCalledWith(SECOND);
    expect(nav.refresh).not.toHaveBeenCalled();
  });

  it("refreshes in place when the released event belongs to the account on screen", async () => {
    const fetchProgress = vi.fn<(id: string) => Promise<ProgressRead>>().mockResolvedValue(progress("complete", allDone()));
    mount({ play: played({ manifest_id: MANIFEST }), fetchProgress });
    press();
    await waitFor(() => expect(nav.refresh).toHaveBeenCalledTimes(1));
    expect(nav.push).not.toHaveBeenCalled();
  });
});

describe("while Play continues into the next account (the manifest on screen has no episode left)", () => {
  const SECOND = "0d3a0000-0000-4000-8000-000000000502";

  it("ignores the old account's finished progress: it is not the result of the new Play", async () => {
    const playGate = deferred();
    const fetchProgress = vi
      .fn<(id: string) => Promise<ProgressRead>>()
      .mockImplementation(async (id) => (id === MANIFEST ? progress("complete", allDone()) : progress("running", { ingest: { status: "running", started_at: "2026-10-05T10:00:00Z" } })));
    const play = vi.fn(async () => {
      await playGate.promise;
      return { ok: true as const, result: advance({ manifest_id: SECOND }) };
    });
    mount({ play, fetchProgress, nextEvent: null });
    press();
    await new Promise((r) => setTimeout(r, POLL * 6)); // several poll periods pass while Play is open
    expect(fetchProgress).not.toHaveBeenCalledWith(MANIFEST); // the old account is not even read
    expect(document.body.textContent).not.toMatch(/pipeline complete|passed|completed/i);
    expect(document.querySelectorAll(".stage.st-waiting")).toHaveLength(7);
    playGate.open();
    await waitFor(() => expect(fetchProgress).toHaveBeenCalledWith(SECOND));
  });

  it("still follows the account's own progress while its own Play is open", async () => {
    const gate = deferred();
    const fetchProgress = vi
      .fn<(id: string) => Promise<ProgressRead>>()
      .mockResolvedValue(progress("running", { ingest: done("ingest"), resolve: { status: "running", started_at: "2026-10-05T10:00:00Z" } }));
    const play = vi.fn(async () => {
      await gate.promise;
      return { ok: true as const, result: advance() };
    });
    mount({ play, fetchProgress });
    press();
    await waitFor(() => expect(stageText("Ingest")).toContain("completed"));
    gate.open();
  });
});
