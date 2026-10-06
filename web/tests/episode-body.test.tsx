// @vitest-environment jsdom
// The episode page bodies (HAR-145): rail selection is URL state; Trace, Graph diff and Raw render the served spans
// verbatim and say so when a trace or diff does not exist. No status ever reads as an eval verdict.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EpisodeRail, GraphDiffBody, NODE_MARK, RawBody, TraceBody, UnknownNodeNote } from "@/components/episode/EpisodeBody";
import { FocusNode } from "@/components/episode/FocusNode";
import type { EpisodeSummary, EpisodeTrace, GraphDiff } from "@/lib/api/types";
import { buildEpisodeView, NODE_WORD, spanOfKind } from "@/lib/view/episode";
import { firstParam } from "@/lib/params";
import { loadExample, loadFixture } from "./contract-validator";

afterEach(cleanup);

const trace = loadExample<EpisodeTrace>("episode_trace");
const summary = loadExample<EpisodeSummary>("episode_summary");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const view = buildEpisodeView(summary, trace);
const EPISODE = summary.id;
const GRAPH = "graph_mutation:41";

describe("EpisodeRail", () => {
  it("renders one link per node carrying node and mode in the URL, marking only the selected", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="trace" selected={GRAPH} />);
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(view.nodes.length);
    expect(links[0]!.getAttribute("href")).toBe(`/episodes/${EPISODE}?node=${encodeURIComponent(view.nodes[0]!.id)}&mode=trace`);
    expect(document.querySelectorAll("[aria-current='true']")).toHaveLength(1);
    expect(document.querySelector("li.selected")!.textContent).toContain("Graph mutation");
  });

  it("gives every node's status a text word (not only a glyph) and never a check mark", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected={null} />);
    const words = [...document.querySelectorAll("li.enode")].map((li) => li.querySelector(".sr-only")?.textContent);
    expect(words).toEqual(view.nodes.map((n) => NODE_WORD[n.status]));
    expect(document.querySelector(".episode-rail")!.textContent).not.toContain("✓");
    expect(Object.values(NODE_MARK)).not.toContain("✓");
  });

  it("shows the three knowledge steps and Influence (not measured) as separate rows", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected={null} />);
    const labels = [...document.querySelectorAll(".nlabel")].map((e) => e.textContent);
    const used = view.nodes.find((n) => n.kind === "knowledge_used")!;
    for (const l of ["Knowledge retrieved", "Knowledge applicable", used.label, "Knowledge influence"]) expect(labels).toContain(l);
    expect(document.querySelector("#node-knowledge_influence")!.textContent).toContain("Influence: not measured");
    expect(document.querySelector("#node-knowledge_influence .sr-only")!.textContent).toBe("not measured");
  });

  it("renders spans by their served status: recorded, pending and not recorded", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected={null} />);
    const cls = (id: string) => document.getElementById(`node-${id}`)!.className;
    expect(cls("precedents:0")).toContain("st-not_recorded");
    expect(cls("cliff_message:judgment")).toContain("st-pending");
    expect(cls("source_event:0ac70000-0000-4000-8000-000000000101")).toContain("st-recorded");
  });

  it("marks nothing when no node is selected", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected={null} />);
    expect(document.querySelectorAll("[aria-current]")).toHaveLength(0);
  });
});

describe("deep link landing (/episodes/:id?node=...)", () => {
  it("gives every rail row a stable id so a node can be addressed", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected={null} />);
    expect(view.nodes.every((n) => document.getElementById(`node-${n.id}`) !== null)).toBe(true);
  });

  it("scrolls the selected node into view and moves keyboard focus to its link", () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    render(
      <>
        <EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected="precedents:0" />
        <FocusNode nodeId="precedents:0" />
      </>,
    );
    expect(document.activeElement).toBe(document.querySelector("[id='node-precedents:0'] a"));
    expect(scroll).toHaveBeenCalledTimes(1);
  });

  it("does nothing for a node that is not on the trajectory", () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    render(<FocusNode nodeId="nope" />);
    expect(scroll).not.toHaveBeenCalled();
  });

  it("says so when ?node= names no node on this trajectory", () => {
    render(<UnknownNodeNote nodes={view.nodes} selected="nope" />);
    expect(document.body.textContent).toContain("No node “nope” on this trajectory");
    cleanup();
    render(<UnknownNodeNote nodes={view.nodes} selected="precedents:0" />);
    expect(document.body.textContent).toBe("");
    cleanup();
    render(<UnknownNodeNote nodes={view.nodes} selected={null} />);
    expect(document.body.textContent).toBe("");
  });
});

describe("TraceBody", () => {
  it("lists every span in order with its status word, rows behind it and eval count", () => {
    render(<TraceBody trace={trace} />);
    const rows = [...document.querySelectorAll("tbody tr")];
    expect(rows).toHaveLength(trace.spans.length);
    expect(rows[0]!.textContent).toContain("Source event");
    expect(rows[0]!.textContent).toContain("recorded");
    const precedents = rows.find((r) => r.textContent!.includes("Precedents"))!;
    expect(precedents.textContent).toContain("not recorded");
    expect(precedents.textContent).toContain("—");
    const candidates = rows.find((r) => r.textContent!.includes("Candidates"))!;
    expect(candidates.querySelector("td:last-child")!.textContent).toBe("2");
  });

  it("wraps the wide table in a focusable, labelled scroll region", () => {
    render(<TraceBody trace={trace} />);
    const region = document.querySelector(".table-scroll")!;
    expect(region.getAttribute("aria-label")).toBe("Trace spans table, scrollable");
    expect(region.getAttribute("tabindex")).toBe("0");
  });

  it("says where eval results with no span of their own went", () => {
    render(<TraceBody trace={trace} unassignedEvalCount={3} />);
    expect(document.body.textContent).toContain("3 eval results (validation and safety checks) belong to no single span.");
    cleanup();
    render(<TraceBody trace={trace} unassignedEvalCount={1} />);
    expect(document.body.textContent).toContain("1 eval result (validation and safety checks) belong to no single span.");
  });

  it("says no trace is recorded when the core has none", () => {
    render(<TraceBody trace={null} />);
    expect(document.body.textContent).toContain("No trace is recorded for this episode.");
    expect(document.querySelectorAll("table")).toHaveLength(0);
  });
});

describe("GraphDiffBody", () => {
  it("renders the projection diff summary and one row per change, then what the state span says changed", () => {
    const state = { ...spanOfKind(trace, "state")!, attributes: { from_version: 6, to_version: 7, changed_fields: ["stage", "health"] } };
    render(<GraphDiffBody diff={diff} stateSpan={state} />);
    expect(document.body.textContent).toContain(`+${diff.summary.added} −${diff.summary.removed} ~${diff.summary.changed} ⟲${diff.summary.repaired}`);
    expect(document.querySelectorAll("tbody tr")).toHaveLength(diff.changes.length);
    expect(document.body.textContent).toContain("account state v");
    expect(within(screen.getByRole("list", { name: "State fields that changed" })).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["stage", "health"]);
  });

  it("says no projection diff was recorded, and that no state change is, when neither exists", () => {
    render(<GraphDiffBody diff={null} stateSpan={null} />);
    expect(document.body.textContent).toContain("No graph projection diff recorded for this event.");
    expect(document.body.textContent).toContain("No account-state change is recorded for this episode.");
    expect(document.querySelectorAll("table")).toHaveLength(0);
  });

  it("does not present a state span that was not recorded as a change", () => {
    render(<GraphDiffBody diff={null} stateSpan={{ ...spanOfKind(trace, "state")!, status: "not_recorded" }} />);
    expect(document.body.textContent).toContain("No account-state change is recorded for this episode.");
  });

  it("omits the version and field lists when the span carries none", () => {
    render(<GraphDiffBody diff={null} stateSpan={{ ...spanOfKind(trace, "state")!, attributes: {} }} />);
    expect(document.body.textContent).not.toContain("account state v");
    expect(screen.queryByRole("list", { name: "State fields that changed" })).toBeNull();
  });

  it("falls back to the raw op for an op it has no mark for", () => {
    const odd = { ...diff, changes: [{ ...diff.changes[0]!, op: "teleported" }] } as unknown as GraphDiff;
    render(<GraphDiffBody diff={odd} stateSpan={null} />);
    expect(within(document.querySelector("tbody")!).getByText("teleported")).toBeTruthy();
  });
});

describe("RawBody", () => {
  it("has a collapsible per node plus the full trace, and the diff only when it exists", () => {
    const { rerender } = render(<RawBody view={view} graphDiff={diff} trace={trace} />);
    expect(document.querySelectorAll("details")).toHaveLength(view.nodes.length + 2);
    rerender(<RawBody view={view} graphDiff={null} trace={trace} />);
    expect(document.querySelectorAll("details")).toHaveLength(view.nodes.length + 1);
    rerender(<RawBody view={view} graphDiff={null} trace={null} />);
    expect(document.querySelectorAll("details")).toHaveLength(view.nodes.length);
    expect(document.querySelector("[id='raw-precedents:0'] pre")!.textContent).toBe(JSON.stringify(view.nodes.find((n) => n.id === "precedents:0")!.data, null, 2));
    expect(document.querySelector("#raw-knowledge_influence pre")!.textContent).toBe("null");
  });
});

describe("firstParam", () => {
  it("returns a string as is, the first of an array, and undefined for nothing", () => {
    expect(firstParam("a")).toBe("a");
    expect(firstParam(["x", "y"])).toBe("x");
    expect(firstParam(undefined)).toBeUndefined();
  });
});
