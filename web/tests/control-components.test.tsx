// @vitest-environment jsdom
// The /control components (HAR-145): the Play strip lights stages only from real artifacts (advance
// result, polled run, posted refs), and the bands/trajectory render honest states.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AdvanceResult, AgentRun } from "@/lib/api/types";
import type { ControlView } from "@/lib/view/control";

const nav = vi.hoisted(() => ({ refresh: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: nav.refresh }) }));

const { PipelineStrip } = await import("@/components/control/PipelineStrip");
const { HealthBands, MaterialChanges, TrajectoryRail } = await import("@/components/control/ControlSections");

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";
const EPISODE = "0de50000-0000-4000-8000-000000000202";

const advance = (over: Partial<AdvanceResult> = {}): AdvanceResult =>
  ({
    manifest_id: MANIFEST,
    account_id: ACCOUNT,
    episode: 3,
    total: 4,
    released: { position: 3, source_system: "email", released: true, held_out: true, material: true },
    material: true,
    no_action_reason: null,
    state_version: 8,
    state_digest: "sha256:x",
    graph_diff_id: 302,
    account_change_id: "0a1c0000-0000-4000-8000-000000000302",
    decision_episode_id: EPISODE,
    coalesced: false,
    advanced_at: "2026-10-04T06:00:01Z",
    ...over,
  }) as AdvanceResult;

const runIn = (phase: string): AgentRun => ({ id: "r", account_id: ACCOUNT, generation: { phase }, steps: [] }) as unknown as AgentRun;
const nextEvent = { position: 3, occurredAt: "2026-10-05T09:00:00Z", source: "email", provenance: "crmarena-pro:b2b" };
const stageText = (label: string) => [...document.querySelectorAll(".stage")].find((li) => li.querySelector(".stage-label")!.textContent === label)!.textContent;

beforeEach(() => {
  vi.useFakeTimers();
  nav.refresh.mockClear();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("PipelineStrip", () => {
  it("starts with every stage waiting and describes the withheld event without its content", () => {
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={vi.fn()} progress={vi.fn()} />);
    expect(document.querySelectorAll(".stage.st-waiting")).toHaveLength(6);
    expect(screen.getByRole("heading").textContent).toBe("Play event 3");
    expect(document.body.textContent).toContain("Held out: email · 2026-10-05T09:00:00Z · crmarena-pro:b2b");
    expect(document.body.textContent).toContain("withheld until release");
  });

  it("disables Play and says everything is released when no event is left", () => {
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext={false} nextEvent={null} play={vi.fn()} progress={vi.fn()} />);
    expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("heading").textContent).toBe("Play event N");
    expect(document.body.textContent).toContain("Every event in the manifest is released.");
  });

  it("shows the refusal code when the core refuses Play and leaves the stages waiting", async () => {
    const play = vi.fn(async () => ({ ok: false as const, code: "no_next_event" }));
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={vi.fn()} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    expect(play).toHaveBeenCalledWith(MANIFEST);
    expect(screen.getByRole("status").textContent).toBe("Play was refused by the core (no_next_event).");
    expect(screen.getByRole("status").className).toContain("error");
    expect(document.querySelectorAll(".stage.st-waiting")).toHaveLength(6);
  });

  it("a non-material event skips downstream stages, states why, and refreshes without polling", async () => {
    const progress = vi.fn();
    const play = vi.fn(async () => ({ ok: true as const, result: advance({ material: false, no_action_reason: "duplicate", decision_episode_id: null }) }));
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={progress} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    expect(screen.getByRole("status").textContent).toBe("Event 3 of 4: no action required (duplicate).");
    expect(document.querySelectorAll(".stage.st-skipped")).toHaveLength(4);
    expect(progress).not.toHaveBeenCalled();
    expect(nav.refresh).toHaveBeenCalledTimes(1);
  });

  it("a non-material event with no stated reason omits the parenthetical", async () => {
    const play = vi.fn(async () => ({ ok: true as const, result: advance({ material: false, no_action_reason: null, decision_episode_id: null }) }));
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={vi.fn()} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    expect(screen.getByRole("status").textContent).toBe("Event 3 of 4: no action required.");
  });

  it("a material event follows the real run: running while generating, then Cliff only once a message is posted", async () => {
    const progress = vi
      .fn()
      .mockResolvedValueOnce({ run: runIn("generating"), cliffPosted: [] })
      .mockResolvedValueOnce({ run: runIn("published"), cliffPosted: [] })
      .mockResolvedValueOnce({ run: runIn("published"), cliffPosted: ["chooser"] });
    const play = vi.fn(async () => ({ ok: true as const, result: advance() }));
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={progress} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    expect(screen.getByRole("status").textContent).toContain("was material");
    expect(stageText("Decide")).toContain("running");
    expect(nav.refresh).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(progress).toHaveBeenCalledWith(MANIFEST, ACCOUNT, EPISODE, "0a1c0000-0000-4000-8000-000000000302");
    expect(stageText("Decide")).toContain("running");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(stageText("Decide")).toContain("passed");
    expect(stageText("Cliff")).toContain("waiting"); // published but nothing posted yet: keep polling, never a pass
    expect(nav.refresh).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(stageText("Cliff")).toContain("chooser posted");
    expect(nav.refresh).toHaveBeenCalledTimes(1);
  });

  it("a core outage during the poll reads 'backend unavailable', never 'running' or an eval failure", async () => {
    const progress = vi.fn().mockResolvedValue({ run: null, cliffPosted: [], unavailable: true });
    const play = vi.fn(async () => ({ ok: true as const, result: advance() }));
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={progress} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(stageText("Decide")).toContain("backend unavailable");
    expect(stageText("Decide")).not.toContain("running");
    expect(stageText("Cliff")).toContain("backend unavailable");
    expect(document.querySelectorAll(".stage.st-failed")).toHaveLength(0);
  });

  it("a transport failure on Play reads 'backend unavailable' in a neutral line, not the eval-failure style", async () => {
    const play = vi.fn(async () => ({ ok: false as const, code: "unreachable" }));
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={vi.fn()} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    expect(screen.getByRole("status").textContent).toBe("Backend unavailable: Play was not delivered, so nothing was released.");
    expect(screen.getByRole("status").className).not.toContain("error");
  });

  it("a progress read that throws leaves Cliff 'not observed' once the run has settled, instead of a pass", async () => {
    const progress = vi.fn().mockRejectedValue(new Error("down"));
    const play = vi.fn(async () => ({ ok: true as const, result: advance() }));
    render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={progress} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    // The read itself failed -> the strip says so; it does not pretend the run is still running.
    expect(stageText("Decide")).toContain("backend unavailable");
    expect(stageText("Cliff")).not.toContain("passed");
  });

  it("stops polling when unmounted", async () => {
    const progress = vi.fn().mockResolvedValue({ run: runIn("generating"), cliffPosted: [] });
    const play = vi.fn(async () => ({ ok: true as const, result: advance() }));
    const { unmount } = render(<PipelineStrip manifestId={MANIFEST} accountId={ACCOUNT} canPlayNext nextEvent={nextEvent} play={play} progress={progress} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Play event N" }));
    });
    unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(progress).not.toHaveBeenCalled();
  });
});

describe("HealthBands", () => {
  it("renders each band's tone word and omits the fact list when empty", () => {
    render(
      <HealthBands
        bands={[
          { id: "intelligence", label: "Intelligence", status: "ok", tone: "ok", facts: ["1 material event"] },
          { id: "decision_learning", label: "Decision & Learning", status: "bad", tone: "fail", facts: [] },
          { id: "cliff_experience", label: "Cliff / Experience", status: "not observable", tone: "none", facts: [] },
          { id: "system", label: "System", status: "careful", tone: "warn", facts: [] },
          { id: "intelligence", label: "Intelligence", status: "unsure", tone: "unsure", facts: [] },
        ]}
      />,
    );
    expect([...document.querySelectorAll(".sr-only")].map((e) => e.textContent)).toEqual(["healthy", "failing", "no signal", "warning", "unsure"]);
    expect(document.querySelectorAll("li")).toHaveLength(1);
  });

  it("shows a check mark only for a real eval verdict tone; recorded and unsure get their own neutral marks", () => {
    render(
      <HealthBands
        bands={[
          { id: "cliff_experience", label: "Cliff / Experience", status: "1 message posted", tone: "recorded", facts: [] },
          { id: "intelligence", label: "Intelligence", status: "unsure", tone: "unsure", facts: [] },
          { id: "decision_learning", label: "Decision & Learning", status: "ok", tone: "ok", facts: [] },
        ]}
      />,
    );
    const marks = [...document.querySelectorAll(".band-mark")].map((e) => e.textContent);
    expect(marks).toEqual(["●", "◌", "✓"]);
    expect([...document.querySelectorAll(".sr-only")].map((e) => e.textContent)).toEqual(["recorded", "unsure", "healthy"]);
  });
});

describe("MaterialChanges", () => {
  it("says nothing changed yet when empty", () => {
    render(<MaterialChanges changes={[]} />);
    expect(document.body.textContent).toContain("No released event has changed the world yet.");
  });

  it("links an episode only when the change opened one", () => {
    render(
      <MaterialChanges
        changes={[
          { position: 1, label: "Email", detail: "state v6", decisionEpisodeId: EPISODE },
          { position: 2, label: "Call", detail: null, decisionEpisodeId: null },
        ]}
      />,
    );
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]!.getAttribute("href")).toBe(`/episodes/${EPISODE}`);
    expect(document.querySelectorAll(".detail")).toHaveLength(1);
  });

  it("carries the manifest so the episode page can tie Message 1 to this change", () => {
    render(<MaterialChanges manifestId={MANIFEST} changes={[{ position: 1, label: "Email", detail: null, decisionEpisodeId: EPISODE }]} />);
    expect(screen.getByRole("link").getAttribute("href")).toBe(`/episodes/${EPISODE}?manifest=${MANIFEST}`);
  });
});

describe("TrajectoryRail", () => {
  const steps: ControlView["trajectory"] = [
    { position: 1, label: "Email", released: true, material: true },
    { position: 2, label: "Call", released: true, material: false },
    { position: 3, label: "Note", released: true, material: null },
    { position: 4, label: "Held", released: false, material: null },
  ] as ControlView["trajectory"];

  it("links released steps to the replay bound and leaves the held-out step inert", () => {
    render(<TrajectoryRail steps={steps} manifestId={MANIFEST} />);
    const links = screen.getAllByRole("link");
    expect(links.map((l) => l.getAttribute("href"))).toEqual([1, 2, 3].map((n) => `/replay/${MANIFEST}?at=${n}`));
    expect(links.map((l) => l.getAttribute("aria-label"))).toEqual(["Episode 1, material", "Episode 2, no action", "Episode 3, released"]);
    expect(screen.getByLabelText("Event 4, held out")).toBeTruthy();
    expect([...document.querySelectorAll(".status")].map((s) => s.textContent)).toEqual(["material", "no action", "released", "held out"]);
  });
});
