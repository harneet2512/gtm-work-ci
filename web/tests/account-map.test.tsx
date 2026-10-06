// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AccountMap } from "@/components/AccountMap";
import { indexClaims } from "@/lib/graph/claim-index";
import { buildExplorerModel } from "@/lib/graph/model";
import { indexDiff } from "@/lib/view/diff";
import type { AccountState, Activity, Graph, GraphDiff } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";
import { fakeContext } from "./graph-canvas-fake";

const graph = loadFixture<Graph>("acme.graph.json");
const diff = loadFixture<GraphDiff>("acme.graph-diff.json");
const state = loadExample<AccountState>("account_state");
const activities = loadFixture<{ items: Activity[] }>("acme.timeline.json").items;
const marks = indexDiff(diff);
const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";
const ACT_101 = "0ac70000-0000-4000-8000-000000000101";
const EVENT = "05e00000-0000-4000-8000-000000000101";

beforeEach(() => {
  const fake = fakeContext();
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation((() => fake.ctx) as never);
  // Reduced motion: camera moves land at once, so the tests read the result synchronously.
  window.matchMedia = ((q: string) => ({ matches: q.includes("reduce"), media: q, addEventListener: () => {}, removeEventListener: () => {} })) as unknown as typeof window.matchMedia;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

type Props = Parameters<typeof AccountMap>[0];

function renderMap(over: { graph?: Graph; showMarks?: boolean; playView?: Props["playView"]; selectedId?: string | null; marks?: Props["marks"] } = {}) {
  const onSelect = vi.fn();
  const onClear = vi.fn();
  const g = over.graph ?? graph;
  const m = over.marks ?? marks;
  const model = buildExplorerModel({ graph: g, marks: m, showMarks: over.showMarks ?? false, activities, state, eventId: EVENT });
  const props: Props = { model, marks: m, playView: over.playView ?? null, claims: indexClaims(state), history: new Map(), selectedId: over.selectedId ?? null, onSelect, onClear, details: <p>Inspector body</p>, coverage: "The graph holds all 5 activities and all 2 facts on record." };
  const view = render(<AccountMap {...props} />);
  const rerender = (selectedId: string | null) => view.rerender(<AccountMap {...props} selectedId={selectedId} />);
  return { onSelect, onClear, model, rerender, container: view.container };
}

describe("AccountMap text alternative", () => {
  it("lists people, company, opportunity, activities and edges from the graph", () => {
    renderMap();
    const map = screen.getByTestId("account-map");
    expect(within(map).getAllByRole("button", { name: /\(Person\)$/ })).toHaveLength(3);
    expect(within(map).getByRole("button", { name: "Acme Corp (Account)" })).toBeTruthy();
    expect(within(map).getByRole("button", { name: /^Acme EU expansion \(Deal\)/ })).toBeTruthy();
    expect(within(map).getAllByRole("button", { name: /^involves: / })).toHaveLength(4);
    expect(within(map).getByRole("button", { name: /^champion for: Priya Shah → Acme EU expansion/ })).toBeTruthy();
    expect(within(map).getByRole("button", { name: /^technical evaluator for: Marco Ruiz/ })).toBeTruthy();
  });

  it("shows no highlight unless asked, and the event's marks After Play", () => {
    const quiet = renderMap();
    expect(quiet.container.querySelectorAll("[data-mark]")).toHaveLength(0);
    cleanup();
    const { container } = renderMap({ showMarks: true, playView: "after" });
    expect(container.querySelector(`[data-node-id="${ACT_101}"]`)?.getAttribute("data-mark")).toBe("added");
    expect(container.querySelector('[data-node-id="0c0f0000-0000-4000-8000-000000000004"]')?.getAttribute("data-mark")).toBe("changed");
    expect(container.querySelector('[data-edge-id="0ed90000-0000-4000-8000-000000000010"]')?.getAttribute("data-mark")).toBe("added");
    expect(container.querySelector('[data-node-id="0b0e0000-0000-4000-8000-000000000017"]')?.getAttribute("data-mark")).toBeNull();
    expect(screen.getByRole("button", { name: /^Email from .*\(Activity\), added$/ })).toBeTruthy();
  });

  it("selects a node with its evidence, connections and what the event did", () => {
    const { onSelect } = renderMap({ showMarks: true, playView: "after" });
    fireEvent.click(screen.getByRole("button", { name: "Acme Corp (Account)" }));
    expect(onSelect).toHaveBeenCalledOnce();
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ kind: "node", id: ACCOUNT });
    expect(onSelect.mock.calls[0]![0].related.length).toBeGreaterThan(0);
  });

  it("reports an edge with both endpoint labels", () => {
    const { onSelect } = renderMap();
    fireEvent.click(screen.getByRole("button", { name: /^champion for: / }));
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ kind: "edge", title: "champion for: Priya Shah → Acme EU expansion" });
  });

  it("marks the selected element", () => {
    renderMap({ selectedId: ACCOUNT });
    expect(screen.getByRole("button", { name: "Acme Corp (Account)" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: /^Priya Shah \(Person\)/ }).getAttribute("aria-pressed")).toBe("false");
  });

  it("labels a withheld node without leaking anything and ignores edges to unknown nodes", () => {
    renderMap({ graph: { ...graph, nodes: [{ ...graph.nodes[0]!, label: "Account", withheld: "visibility" }], edges: [graph.edges[0]!] } });
    expect(screen.getByRole("button", { name: /withheld/i })).toBeTruthy();
    expect(screen.queryAllByRole("button", { name: /^works at/ })).toHaveLength(0);
  });

  it("explains an empty graph", () => {
    renderMap({ graph: { ...graph, nodes: [], edges: [] } });
    expect(screen.getByText(/No graph around this account yet/)).toBeTruthy();
  });
});

describe("AccountMap explorer", () => {
  it("renders a focusable canvas with instructions and the kinds legend", () => {
    renderMap();
    const canvas = screen.getByRole("application", { name: "Account graph" });
    expect(canvas.getAttribute("tabindex")).toBe("0");
    expect(document.getElementById(canvas.getAttribute("aria-describedby")!)!.textContent).toMatch(/Left and right arrows/);
    expect(within(screen.getByRole("list", { name: "Node kinds" })).getByText("Person")).toBeTruthy();
  });

  it("steps through activities in time order with the arrow keys", () => {
    const { onSelect } = renderMap();
    const canvas = screen.getByRole("application", { name: "Account graph" });
    fireEvent.keyDown(canvas, { key: "ArrowRight" });
    expect(onSelect.mock.calls[0]![0].meta).toMatch(/^Activity/);
    fireEvent.keyDown(canvas, { key: "ArrowLeft" });
    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it("backs out with Escape and zooms with the keys and buttons", () => {
    const { onClear } = renderMap({ selectedId: ACCOUNT });
    const canvas = screen.getByRole("application", { name: "Account graph" });
    const stage = canvas.parentElement!;
    fireEvent.keyDown(canvas, { key: "Escape" });
    expect(onClear).toHaveBeenCalledOnce();
    const k = () => Number(stage.dataset.zoom);
    const fitted = k();
    fireEvent.keyDown(canvas, { key: "+" });
    expect(k()).toBeGreaterThan(fitted);
    fireEvent.keyDown(canvas, { key: "-" });
    fireEvent.keyDown(canvas, { key: "0" });
    expect(k()).toBeCloseTo(fitted);
    fireEvent.click(screen.getByRole("button", { name: "Zoom in" }));
    expect(k()).toBeGreaterThan(fitted);
    fireEvent.click(screen.getByRole("button", { name: "Zoom out" }));
    fireEvent.click(screen.getByRole("button", { name: "Fit the whole graph" }));
    expect(k()).toBeCloseTo(fitted);
    fireEvent.keyDown(canvas, { key: "Enter" });
    expect(k()).toBeGreaterThan(fitted);
  });

  it("finds a node with Ctrl+K and jumps to it", () => {
    const { onSelect } = renderMap();
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    const box = screen.getByRole("combobox", { name: "Find a node in the graph" });
    expect(document.activeElement).toBe(box);
    fireEvent.change(box, { target: { value: "priya" } });
    expect(screen.getByRole("option", { name: "Priya Shah (Person)" })).toBeTruthy();
    // The best match is preselected; Down then Up comes back to it.
    fireEvent.keyDown(box, { key: "ArrowDown" });
    fireEvent.keyDown(box, { key: "ArrowUp" });
    fireEvent.keyDown(box, { key: "Enter" });
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ title: "Priya Shah" });
    fireEvent.change(box, { target: { value: "zzzz" } });
    expect(screen.getByText("No node matches.")).toBeTruthy();
    fireEvent.keyDown(box, { key: "Escape" });
    expect((box as HTMLInputElement).value).toBe("");
  });

  it("keeps a trail of visited nodes that leads back", () => {
    const { rerender, onSelect } = renderMap();
    act(() => rerender(ACCOUNT));
    act(() => rerender(ACT_101));
    const trail = screen.getByRole("navigation", { name: "Visited nodes" });
    const crumbs = within(trail).getAllByRole("button");
    expect(crumbs).toHaveLength(2);
    expect(crumbs[1]!.getAttribute("aria-current")).toBe("true");
    fireEvent.click(within(trail).getByRole("button", { name: "Back to Acme Corp (Account)" }));
    expect(onSelect.mock.calls.at(-1)![0]).toMatchObject({ id: ACCOUNT });
  });

  it("lists what the event changed beside the canvas, After Play only", () => {
    const { onSelect } = renderMap({ showMarks: true, playView: "after" });
    const panel = screen.getByTestId("graph-diff");
    expect(within(panel).getByText("What this event changed")).toBeTruthy();
    expect(within(panel).getByRole("region", { name: "Added" })).toBeTruthy();
    fireEvent.click(within(panel).getAllByRole("button")[0]!);
    expect(onSelect).toHaveBeenCalled();
    cleanup();
    renderMap({ playView: "before" });
    expect(screen.queryByTestId("graph-diff")).toBeNull();
  });

  it("says the graph change was not recorded when the event has no projection", () => {
    renderMap({ showMarks: false, playView: "after", marks: indexDiff({ ...diff, projected: false }) });
    expect(within(screen.getByTestId("graph-diff")).getByText("Graph change not recorded.")).toBeTruthy();
  });
});

describe("AccountMap layout", () => {
  it("shows the inspector beside the canvas, and says what the graph holds", () => {
    renderMap();
    expect(within(screen.getByRole("region", { name: "Provenance" })).getByText("Inspector body")).toBeTruthy();
    expect(screen.getByText("The graph holds all 5 activities and all 2 facts on record.")).toBeTruthy();
  });

  it("expands to fill the workspace with the button or F, and Escape comes back before it clears", () => {
    const { onClear } = renderMap({ selectedId: ACCOUNT });
    const root = screen.getByTestId("account-map");
    const expand = screen.getByRole("button", { name: "Expand the graph" });
    fireEvent.click(expand);
    expect(root.className).toContain("is-expanded");
    expect(screen.getByRole("button", { name: "Leave the expanded view" }).getAttribute("aria-pressed")).toBe("true");
    const canvas = screen.getByRole("application", { name: "Account graph" });
    fireEvent.keyDown(canvas, { key: "Escape" });
    expect(root.className).not.toContain("is-expanded");
    expect(onClear).not.toHaveBeenCalled();
    fireEvent.keyDown(canvas, { key: "f" });
    expect(root.className).toContain("is-expanded");
    fireEvent.keyDown(canvas, { key: "F" });
    expect(root.className).not.toContain("is-expanded");
    fireEvent.keyDown(canvas, { key: "Escape" });
    expect(onClear).toHaveBeenCalledOnce();
  });

  it("explains the status marks in words in the legend After Play", () => {
    renderMap({ showMarks: true, playView: "after" });
    const legend = screen.getByRole("list", { name: "Node kinds" });
    for (const word of ["Added", "Changed", "Removed"]) expect(within(legend).getByText(word)).toBeTruthy();
    cleanup();
    renderMap();
    expect(within(screen.getByRole("list", { name: "Node kinds" })).queryByText("Added")).toBeNull();
  });
});
