// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AccountMap } from "@/components/AccountMap";
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

describe("AccountMap", () => {
  const renderMap = (over: Partial<Parameters<typeof AccountMap>[0]> = {}) => {
    const onSelect = vi.fn();
    render(<AccountMap graph={graph} marks={marks} showMarks={false} selectedId={null} onSelect={onSelect} {...over} />);
    return onSelect;
  };

  it("draws people, roles, company, opportunity, activities and INVOLVES edges from the graph", () => {
    renderMap();
    expect(screen.getAllByRole("button", { name: /^Person: / })).toHaveLength(3);
    expect(screen.getByRole("button", { name: "Account: Acme Corp" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Opportunity: Acme EU expansion/ })).toBeTruthy();
    expect(screen.getAllByRole("button", { name: /^INVOLVES: / })).toHaveLength(4);
    expect(screen.getByRole("button", { name: /^CHAMPION_FOR: Priya Shah -> Acme EU expansion/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^TECHNICAL_EVALUATOR_FOR: Marco Ruiz/ })).toBeTruthy();
  });

  it("shows no highlight unless asked", () => {
    const { container } = render(<AccountMap graph={graph} marks={marks} showMarks={false} selectedId={null} onSelect={() => {}} />);
    expect(container.querySelectorAll("[data-mark]")).toHaveLength(0);
  });

  it("highlights added and changed elements After Play", () => {
    const { container } = render(<AccountMap graph={graph} marks={marks} showMarks selectedId={null} onSelect={() => {}} />);
    expect(container.querySelector(`[data-node-id="${ACT_101}"]`)?.getAttribute("data-mark")).toBe("added");
    expect(container.querySelector('[data-node-id="0c0f0000-0000-4000-8000-000000000004"]')?.getAttribute("data-mark")).toBe("changed");
    expect(container.querySelector('[data-edge-id="0ed90000-0000-4000-8000-000000000010"]')?.getAttribute("data-mark")).toBe("added");
    expect(container.querySelector('[data-node-id="0b0e0000-0000-4000-8000-000000000017"]')?.getAttribute("data-mark")).toBeNull();
    expect(screen.getByRole("button", { name: /Activity: EmailReceived.*added/ })).toBeTruthy();
  });

  it("reports the node that was clicked", () => {
    const onSelect = renderMap();
    fireEvent.click(screen.getByRole("button", { name: "Account: Acme Corp" }));
    expect(onSelect).toHaveBeenCalledOnce();
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ kind: "node", id: "0a0c0000-0000-4000-8000-000000000001" });
  });

  it("is operable from the keyboard", () => {
    const onSelect = renderMap();
    const node = screen.getByRole("button", { name: "Account: Acme Corp" });
    fireEvent.keyDown(node, { key: "Enter" });
    fireEvent.keyDown(node, { key: " " });
    fireEvent.keyDown(node, { key: "a" });
    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it("reports an edge click with both endpoint labels", () => {
    const onSelect = renderMap();
    fireEvent.click(screen.getByRole("button", { name: /^CHAMPION_FOR: / }));
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ kind: "edge", title: "CHAMPION_FOR: Priya Shah -> Acme EU expansion" });
  });

  it("marks the selected element", () => {
    renderMap({ selectedId: "0a0c0000-0000-4000-8000-000000000001" });
    expect(screen.getByRole("button", { name: "Account: Acme Corp" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: /^Person: Priya/ }).getAttribute("aria-pressed")).toBe("false");
  });

  it("labels a withheld node without leaking anything and ignores edges to unknown nodes", () => {
    const withheld: Graph = {
      ...graph,
      nodes: [{ ...graph.nodes[0]!, label: "Account", withheld: "visibility" }],
      edges: [graph.edges[0]!],
    };
    renderMap({ graph: withheld });
    expect(screen.getByRole("button", { name: /withheld/i })).toBeTruthy();
    expect(screen.queryAllByRole("button", { name: /^WORKS_AT/ })).toHaveLength(0);
  });

  it("explains an empty graph", () => {
    renderMap({ graph: { ...graph, nodes: [], edges: [] } });
    expect(screen.getByText(/No graph around this account yet/)).toBeTruthy();
  });
});

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
    fireEvent.click(screen.getByRole("button", { name: /MeetingCompleted/ }));
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ kind: "node", refs: [{ activityId: "0ac70000-0000-4000-8000-000000000090" }] });
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

  it("shows the evidence activity: type, source, time, summary and quote", () => {
    const sel = claimSelection("health", state.fields["health"]!);
    render(<ProvenancePanel provenance={resolveProvenance(sel, lookup)} />);
    expect(screen.getByRole("heading", { name: "Claim: health" })).toBeTruthy();
    expect(screen.getByText(/first_party_ai/)).toBeTruthy();
    expect(screen.getByText("EmailReceived")).toBeTruthy();
    expect(screen.getByText(/Marco \(Acme security\) requires SOC2/)).toBeTruthy();
    expect(screen.getByText(/we'll need your SOC2 Type II report/)).toBeTruthy();
    expect(screen.getByText(/email · <CAMx9-acme-2026-09-29@mail.acme.com>/)).toBeTruthy();
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
  it("counts, lists changed properties and removed elements", () => {
    render(<DiffSummary marks={marks} labels={new Map([["0b0e0000-0000-4000-8000-000000000018", "Marco Ruiz"], ["0c0f0000-0000-4000-8000-000000000004", "Acme EU expansion"]])} />);
    expect(screen.getByText("10 added")).toBeTruthy();
    expect(screen.getByText("1 changed")).toBeTruthy();
    expect(screen.getByText("1 removed")).toBeTruthy();
    expect(screen.getByText(/health: unknown -> at_risk/)).toBeTruthy();
    expect(screen.getByText(/INFLUENCES: Marco Ruiz -> Acme EU expansion/)).toBeTruthy();
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
