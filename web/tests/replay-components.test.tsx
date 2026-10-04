// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EpisodeBadges } from "@/components/replay/EpisodeBadges";
import { EpisodeDetail } from "@/components/replay/EpisodeDetail";
import { EpisodeRail } from "@/components/replay/EpisodeRail";
import { ManifestForm } from "@/components/replay/ManifestForm";
import { ReplayControls } from "@/components/replay/ReplayControls";
import { RunTable } from "@/components/RunTable";
import type { AgentRun, EpisodeEvent, EpisodeReplayView } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const refresh = vi.fn();
const push = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh, push }),
}));

afterEach(cleanup);
beforeEach(() => {
  refresh.mockReset();
  push.mockReset();
});

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const view = loadFixture<EpisodeReplayView>("replay.episodes.json");
const [ep1, ep2] = view.prior_episodes as [EpisodeEvent, EpisodeEvent];
const withheld = view.next_event!;

describe("EpisodeBadges", () => {
  it("renders Material for a material episode", () => {
    render(<EpisodeBadges event={ep1} />);
    expect(screen.getByTestId("episode-badges").getAttribute("data-kind")).toBe("material");
    expect(screen.getByText("Material")).toBeTruthy();
  });

  it("renders the no-action reason and the coalesced fold marker", () => {
    render(<EpisodeBadges event={ep2} />);
    expect(screen.getByTestId("episode-badges").getAttribute("data-kind")).toBe("no_action");
    expect(screen.getByText("No action required")).toBeTruthy();
    expect(screen.getByText("no_material_change")).toBeTruthy();
    expect(screen.getByText("coalesced fold")).toBeTruthy();
  });

  it("renders Withheld and the held-out marker for the held-out event", () => {
    const heldOut: EpisodeEvent = { ...withheld, held_out: true };
    render(<EpisodeBadges event={heldOut} />);
    expect(screen.getByTestId("episode-badges").getAttribute("data-kind")).toBe("withheld");
    expect(screen.getByText("Withheld")).toBeTruthy();
    expect(screen.getByText("held-out")).toBeTruthy();
  });
});

describe("EpisodeRail", () => {
  it("lists the boundary, released, withheld and future slots in order", () => {
    render(<EpisodeRail view={view} manifestId={MANIFEST} />);
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(5);
    expect(items[0]!.textContent).toContain("Before the first event");
    expect(items[1]!.textContent).toContain("Material");
    expect(items[2]!.textContent).toContain("No action required");
    expect(items[2]!.textContent).toContain("no_material_change");
    expect(items[3]!.textContent).toContain("Withheld");
    expect(items[4]!.textContent).toContain("Not yet reached");
    expect(items[4]!.textContent).toContain("(held-out window)");
  });

  it("makes the withheld consequences legible and names no future event", () => {
    render(<EpisodeRail view={view} manifestId={MANIFEST} />);
    const card = screen.getByTestId("withheld-card");
    expect(card.textContent).toContain("#3");
    expect(card.textContent).toContain("Consequences are hidden");
    // The withheld card shows only identity fields — never a verdict or a link to one.
    expect(card.textContent).not.toContain("Material");
    expect(within(card).queryAllByRole("link")).toHaveLength(0);
    // The future slot names no event.
    const future = screen.getAllByRole("listitem")[4]!;
    expect(future.querySelectorAll("code")).toHaveLength(0);
  });

  it("links released positions to their ?at view and marks the current one", () => {
    render(<EpisodeRail view={view} manifestId={MANIFEST} />);
    const links = screen.getAllByRole("link");
    const hrefs = links.map((a) => a.getAttribute("href"));
    expect(hrefs).toContain(`/replay/${MANIFEST}?at=1`);
    expect(hrefs).toContain(`/replay/${MANIFEST}?at=2`);
    expect(hrefs).toContain(`/replay/${MANIFEST}?at=0`);
    const current = links.find((a) => a.getAttribute("aria-current") === "page");
    expect(current?.getAttribute("href")).toBe(`/replay/${MANIFEST}?at=2`);
  });

  it("at k=0 selects the boundary and withholds the first event", () => {
    const atZero: EpisodeReplayView = { ...view, episode: 0, window: "none", state: null, prior_episodes: [], next_event: { ...withheld, position: 1 }, can_previous: false };
    render(<EpisodeRail view={atZero} manifestId={MANIFEST} />);
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(5);
    expect(items[1]!.textContent).toContain("Withheld");
    expect(within(items[1]!).getByText("#1")).toBeTruthy();
    expect(items[4]!.textContent).toContain("Not yet reached");
  });

  it("a fully released replay has no withheld or future slots", () => {
    const done: EpisodeReplayView = {
      ...view,
      episode: 4,
      window: "live",
      prior_episodes: [
        ...view.prior_episodes,
        { ...withheld, position: 3, released: true, material: true },
        { ...withheld, position: 4, held_out: true, released: true, material: true },
      ],
      next_event: null,
      can_play_next: false,
    };
    render(<EpisodeRail view={done} manifestId={MANIFEST} />);
    expect(screen.queryByTestId("withheld-card")).toBeNull();
    expect(screen.getAllByRole("listitem")).toHaveLength(5);
    expect(screen.getAllByText("held-out")).toHaveLength(1);
  });
});

describe("EpisodeDetail", () => {
  it("shows the boundary episode's bookkeeping, state digest and knowledge", () => {
    render(<EpisodeDetail view={view} at={null} />);
    expect(screen.getByRole("heading", { name: /Episode 2/ })).toBeTruthy();
    expect(screen.getAllByText("v7").length).toBeGreaterThanOrEqual(2); // episode bound + state version
    expect(screen.getByText(/b1a2c3d4/)).toBeTruthy();
    expect(screen.getByText("diff #40")).toBeTruthy();
    expect(screen.getAllByText("no_material_change").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText(/fold's shared verdict/)).toBeTruthy();
    expect(screen.getByText("Security questionnaires stall without the SOC2 packet attached")).toBeTruthy();
    expect(screen.getByText("supported")).toBeTruthy();
  });

  it("marks the read-only review and announces the withheld next event", () => {
    render(<EpisodeDetail view={view} at={1} />);
    expect(screen.getByText(/viewing an earlier released position \(read-only\)/)).toBeTruthy();
    const next = screen.getByTestId("detail-next");
    expect(next.textContent).toContain("#3");
    expect(next.textContent).toContain("hidden until release");
  });

  it("at k=0 says nothing is released and hides the state", () => {
    const atZero: EpisodeReplayView = {
      ...view,
      episode: 0,
      window: "none",
      state: null,
      knowledge: { as_of: null, items: [] },
      prior_episodes: [],
      next_event: { ...withheld, position: 1 },
      can_previous: false,
    };
    render(<EpisodeDetail view={atZero} at={0} />);
    expect(screen.getByText(/No event has been released at this boundary/)).toBeTruthy();
    expect(screen.getByText(/No state exists before the first event/)).toBeTruthy();
    expect(screen.getByText(/No knowledge is applicable/)).toBeTruthy();
  });

  it("at the complete boundary announces the finished replay", () => {
    const done: EpisodeReplayView = { ...view, episode: 4, window: "live", next_event: null, can_play_next: false };
    render(<EpisodeDetail view={done} at={null} />);
    expect(screen.getByText(/replay is complete/)).toBeTruthy();
  });
});

describe("ReplayControls", () => {
  const props = { manifestId: MANIFEST, canPlayNext: true, canReset: true };

  it("plays the next event and reports the core's verdict", async () => {
    const advance = vi.fn(async () => ({ ok: true as const, episode: 3, total: 4, material: true, noActionReason: null, stateVersion: 8, coalesced: false }));
    render(<ReplayControls {...props} advance={advance} reset={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Play next" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("Released episode 3 of 4: material · state v8"));
    expect(advance).toHaveBeenCalledWith(MANIFEST);
    expect(refresh).toHaveBeenCalled();
  });

  it("reports a no-action release with its reason", async () => {
    const advance = vi.fn(async () => ({ ok: true as const, episode: 3, total: 4, material: false, noActionReason: "no_material_change", stateVersion: null, coalesced: true }));
    render(<ReplayControls {...props} advance={advance} reset={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Play next" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("No action required (no_material_change) · no state · coalesced fold"));
  });

  it("shows the refusal code when the core rejects the advance", async () => {
    const advance = vi.fn(async () => ({ ok: false as const, code: "replay_complete" }));
    render(<ReplayControls {...props} advance={advance} reset={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Play next" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("replay_complete"));
    expect(refresh).not.toHaveBeenCalled();
  });

  it("asks before resetting, then resets to episode 0", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const reset = vi.fn(async () => ({ ok: true as const, episode: 0 }));
    render(<ReplayControls {...props} advance={vi.fn()} reset={reset} />);
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));
    expect(confirm).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("reset to episode 0"));
    expect(reset).toHaveBeenCalledWith(MANIFEST, 0);
    expect(refresh).toHaveBeenCalled();
    confirm.mockRestore();
  });

  it("does not reset when the confirm is declined", () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const reset = vi.fn();
    render(<ReplayControls {...props} advance={vi.fn()} reset={reset} />);
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));
    expect(reset).not.toHaveBeenCalled();
    confirm.mockRestore();
  });

  it("disables the controls at the replay bounds", () => {
    render(<ReplayControls manifestId={MANIFEST} canPlayNext={false} canReset={false} advance={vi.fn()} reset={vi.fn()} />);
    expect((screen.getByRole("button", { name: "Play next" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Reset" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("ManifestForm", () => {
  it("navigates to the typed manifest's replay page", async () => {
    const user = userEvent.setup();
    render(<ManifestForm />);
    await user.type(screen.getByLabelText("Demo manifest id"), MANIFEST.toUpperCase());
    await user.click(screen.getByRole("button", { name: "Open replay" }));
    expect(push).toHaveBeenCalledWith(`/replay/${MANIFEST}`);
  });

  it("rejects a non-uuid instead of navigating", async () => {
    const user = userEvent.setup();
    render(<ManifestForm />);
    await user.type(screen.getByLabelText("Demo manifest id"), "not-a-uuid");
    await user.click(screen.getByRole("button", { name: "Open replay" }));
    expect(push).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toContain("uuid");
  });
});

describe("RunTable", () => {
  it("renders the runs with status and generation phase", () => {
    const run = loadExample<AgentRun>("agent_run");
    render(<RunTable runs={[run]} />);
    expect(screen.getByText("post_interaction_followup")).toBeTruthy();
    expect(screen.getByText("dry_run")).toBeTruthy();
    expect(screen.getByText("awaiting_human")).toBeTruthy();
    expect(screen.getByText("published")).toBeTruthy();
    // The run id links into the chain page (WP24); the account id into the account.
    const links = screen.getAllByRole("link").map((l) => l.getAttribute("href"));
    expect(links).toEqual([`/runs/${run.id}`, `/accounts/${run.account_id}`]);
  });

  it("explains an empty run list", () => {
    render(<RunTable runs={[]} />);
    expect(screen.getByText(/No agent runs yet/)).toBeTruthy();
  });
});
