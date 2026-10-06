// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ClaimsList } from "@/components/ClaimsList";
import { DiffSummary } from "@/components/DiffSummary";
import { ProvenancePanel } from "@/components/ProvenancePanel";
import { Timeline } from "@/components/Timeline";
import { TransitionBadgeView } from "@/components/TransitionBadgeView";
import { indexDiff } from "@/lib/view/diff";
import { transitionBadge } from "@/lib/view/transition";
import { claimSelection, nodeSelection, resolveProvenance } from "@/lib/view/provenance";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

afterEach(cleanup);

const graph = loadFixture<Graph>("acme.graph.json");
const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
const state = loadExample<AccountState>("account_state");
const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;
const marks = indexDiff(diff);
const ACT_101 = "0ac70000-0000-4000-8000-000000000101";

describe("Timeline", () => {
  it("lists activities oldest first", () => {
    render(<Timeline activities={[activities[4]!, activities[3]!]} cutoff={null} eventActivityIds={new Set()} selectedId={null} onSelect={() => {}} />);
    const rows = screen.getAllByRole("listitem");
    expect(rows[0]!.textContent).toContain("Kickoff");
    expect(rows[1]!.textContent).toContain("Technical evaluation");
  });

  it("states the cutoff and flags the event's activities", () => {
    render(<Timeline activities={activities} cutoff="2026-09-29T15:42:00.000Z" eventActivityIds={new Set([ACT_101])} selectedId={null} onSelect={() => {}} />);
    expect(screen.getByText(/through 2026-09-29 15:42 UTC/)).toBeTruthy();
    const flagged = screen.getAllByText("from this event");
    expect(flagged).toHaveLength(1);
    expect(within(flagged[0]!.closest("li")!).getByText(/SOC2/)).toBeTruthy();
  });

  it("selects an activity on click", () => {
    const onSelect = vi.fn();
    render(<Timeline activities={activities} cutoff={null} eventActivityIds={new Set()} selectedId={null} onSelect={onSelect} />);
    fireEvent.click(screen.getByRole("button", { name: /Meeting with Priya/ }));
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ kind: "node", title: "Meeting with Priya · Sep 18, 2026", refs: [{ activityId: "0ac70000-0000-4000-8000-000000000090" }] });
    expect(screen.queryByText("MeetingCompleted")).toBeNull();
  });

  it("explains an empty timeline", () => {
    render(<Timeline activities={[]} cutoff={null} eventActivityIds={new Set()} selectedId={null} onSelect={() => {}} />);
    expect(screen.getByText(/No activities/)).toBeTruthy();
  });
});

describe("ProvenancePanel", () => {
  const lookup = new Map(activities.map((a) => [a.id, a]));

  it("asks for a click when nothing is selected", () => {
    render(<ProvenancePanel provenance={null} />);
    expect(screen.getByText(/Click a node, edge, claim or activity/)).toBeTruthy();
  });

  it("shows each piece of evidence with chips: who said it, the source record and the standing", () => {
    const sel = claimSelection("health", state.fields["health"]!);
    render(<ProvenancePanel provenance={resolveProvenance(sel, lookup)} />);
    expect(screen.getByRole("heading", { name: "Health: at risk" })).toBeTruthy();
    expect(screen.getAllByText("AI reading of first-party evidence").length).toBeGreaterThan(0);
    expect(screen.queryByText(/first_party_ai/)).toBeNull();
    expect(screen.getByText(/Marco \(Acme security\) requires SOC2/)).toBeTruthy();
    expect(screen.getByText(/we'll need your SOC2 Type II report/)).toBeTruthy();
    expect(screen.getByText("Said by Marco Ruiz")).toBeTruthy();
    expect(screen.getByText("Email · Sep 29, 2026")).toBeTruthy();
    // Raw source ids stay available, behind a disclosure.
    expect(screen.getByText("Source details")).toBeTruthy();
    expect(screen.getByText(/email · <CAMx9-acme-2026-09-29@mail.acme.com>/).closest("details")).not.toBeNull();
  });

  it("labels an outranked claim as retained", () => {
    const sel = { ...claimSelection("health", state.fields["health"]!), facts: [{ label: "Standing", value: "Outranked, retained" }] };
    render(<ProvenancePanel provenance={resolveProvenance(sel, lookup)} />);
    expect(screen.getByText("Outranked, retained")).toBeTruthy();
  });

  it("keeps an evidence id visible when its activity is outside the loaded window", () => {
    const node = graph.nodes.find((n) => n.id === ACT_101)!;
    render(<ProvenancePanel provenance={resolveProvenance(nodeSelection(node), new Map())} />);
    expect(screen.getByText(ACT_101)).toBeTruthy();
    expect(screen.getByText(/not in the loaded timeline/)).toBeTruthy();
  });

  it("explains withheld evidence and elements without evidence", () => {
    const withheld = { ...graph.nodes[0]!, withheld: "visibility" as const };
    const { unmount } = render(<ProvenancePanel provenance={resolveProvenance(nodeSelection(withheld), lookup)} />);
    expect(screen.getByText(/hidden by visibility rules/)).toBeTruthy();
    unmount();
    render(<ProvenancePanel provenance={resolveProvenance(nodeSelection(graph.nodes[0]!), lookup)} />);
    expect(screen.getByText(/No evidence is recorded/)).toBeTruthy();
  });
});

describe("TransitionBadgeView", () => {
  it("renders the kind, the transition and what is still missing", () => {
    render(<TransitionBadgeView badge={transitionBadge(state)} />);
    const badge = screen.getByTestId("transition-badge");
    expect(badge.getAttribute("data-kind")).toBe("CANDIDATE");
    expect(badge.textContent).toContain("REORG -> EXPANSION");
    expect(badge.textContent).toContain("owner_stabilized");
    expect(badge.textContent).toContain("0.40");
  });

  it("renders nothing loud for none", () => {
    render(<TransitionBadgeView badge={transitionBadge(null)} />);
    expect(screen.getByTestId("transition-badge").getAttribute("data-kind")).toBe("none");
  });
});

describe("DiffSummary", () => {
  it("says the graph change was not recorded when the event has no projection", () => {
    render(<DiffSummary marks={indexDiff({ ...diff, projected: false })} labels={new Map()} />);
    expect(screen.getByText(/^Graph change not recorded/)).toBeTruthy();
  });

  it("counts, lists changed properties and removed elements", () => {
    render(<DiffSummary marks={marks} labels={new Map([["0b0e0000-0000-4000-8000-000000000018", "Marco Ruiz"], ["0c0f0000-0000-4000-8000-000000000004", "Acme EU expansion"]])} />);
    expect(screen.getByText("10 added")).toBeTruthy();
    expect(screen.getByText("1 changed")).toBeTruthy();
    expect(screen.getByText("1 removed")).toBeTruthy();
    expect(screen.getByText(/health: unknown → at risk/)).toBeTruthy();
    expect(screen.getByText(/influences: Marco Ruiz → Acme EU expansion/)).toBeTruthy();
    expect(screen.queryByText(/INFLUENCES|->/)).toBeNull();
  });

  it("says when the event has not been projected yet", () => {
    render(<DiffSummary marks={indexDiff(null)} labels={new Map()} />);
    expect(screen.getByText(/not projected/)).toBeTruthy();
  });
});

describe("ClaimsList", () => {
  it("lists known fields with value and standing and selects on click", () => {
    const onSelect = vi.fn();
    render(<ClaimsList fields={state.fields} selectedId={null} onSelect={onSelect} />);
    fireEvent.click(screen.getByRole("button", { name: /health/ }));
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ kind: "claim", id: "claim:health" });
    expect(screen.getByText("at_risk")).toBeTruthy();
  });

  it("skips fields that have no evidence or are unknown", () => {
    render(<ClaimsList fields={{ ghost: { ...state.fields["health"]!, known: false }, bare: { ...state.fields["health"]!, evidence_refs: [] } }} selectedId={null} onSelect={() => {}} />);
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(screen.getByText(/No claims/)).toBeTruthy();
  });
});
