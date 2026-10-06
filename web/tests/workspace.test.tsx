// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AccountWorkspace } from "@/components/AccountWorkspace";
import { buildAccountView } from "@/lib/view/account-view";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

afterEach(cleanup);

const graph = loadFixture<Graph>("acme.graph.json");
const graphBefore = loadFixture<Graph>("acme.graph-before.json");
const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
const state = loadExample<AccountState>("account_state");
const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;
const EVENT = "05e00000-0000-4000-8000-000000000101";

const CUTOFF = "2026-09-29T15:42:00.000Z";
const view = (v: "before" | "after", eventId: string | null = EVENT, g: Graph = graph) =>
  buildAccountView({ graph: g, diff, activities, view: v, cutoff: v === "before" ? CUTOFF : null, eventId });

describe("AccountWorkspace Before Play", () => {
  it("shows the world map (no event additions), the cut timeline and no highlights", () => {
    const { container } = render(<AccountWorkspace view={view("before", EVENT, graphBefore)} state={state} activities={activities} hasEvent />);
    expect(screen.getByRole("heading", { name: "Before Play: account map" })).toBeTruthy();
    // Event N's email (Sep 29) is not in the world before it.
    expect(screen.queryByRole("button", { name: /^Email from Marco · Sep 29, 2026 \(Activity\)/ })).toBeNull();
    expect(screen.getAllByRole("button", { name: /\(Activity\)$/ }).length).toBeGreaterThan(0);
    expect(container.querySelectorAll("[data-mark]")).toHaveLength(0);
    expect(within(screen.getByRole("region", { name: "Timeline" })).getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getByText(/Switch to After Play/)).toBeTruthy();
  });
});

describe("AccountWorkspace After Play", () => {
  it("highlights the diff and lists it", () => {
    const { container } = render(<AccountWorkspace view={view("after")} state={state} activities={activities} hasEvent />);
    expect(container.querySelectorAll('[data-node-id][data-mark="added"]')).toHaveLength(3);
    expect(screen.getByText("10 added")).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "Timeline" })).getByText("from this event")).toBeTruthy();
  });

  it("omits the diff panel when no event is inspected", () => {
    render(<AccountWorkspace view={view("after", null)} state={state} activities={activities} hasEvent={false} />);
    expect(screen.queryByRole("region", { name: /Graph diff/ })).toBeNull();
  });
});

describe("provenance by click", () => {
  const panel = () => screen.getByRole("region", { name: "Provenance" });

  it("a graph node opens the activity it came from", () => {
    render(<AccountWorkspace view={view("after")} state={state} activities={activities} hasEvent />);
    expect(within(panel()).getByText(/Click a node/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^Health: at risk \(Fact\)/ }));
    expect(within(panel()).getByRole("heading", { name: "Health: at risk" })).toBeTruthy();
    expect(within(panel()).getByText(/Marco \(Acme security\) requires SOC2/)).toBeTruthy();
  });

  it("a claim opens its quote, and a timeline item replaces the selection", () => {
    render(<AccountWorkspace view={view("after")} state={state} activities={activities} hasEvent />);
    fireEvent.click(screen.getByRole("button", { name: "Claim health" }));
    expect(within(panel()).getByText(/we'll need your SOC2 Type II report/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Meeting with Priya/ }));
    expect(within(panel()).getByRole("heading", { name: "Meeting with Priya · Sep 18, 2026" })).toBeTruthy();
    expect(within(panel()).getByText(/Kickoff for the EU rollout/)).toBeTruthy();
  });
});

describe("degraded inputs", () => {
  it("renders without account state and with a lagging, truncated projection", () => {
    const lagging: Graph = { ...graph, truncated: true, projection: { complete: false, projected_at: null } };
    render(<AccountWorkspace view={view("after", null, lagging)} state={null} activities={activities} hasEvent={false} />);
    expect(screen.getByText(/No account state computed yet/)).toBeTruthy();
    expect(screen.getByText(/still catching up/)).toBeTruthy();
    expect(screen.getByText(/truncated this neighborhood/)).toBeTruthy();
  });
});
