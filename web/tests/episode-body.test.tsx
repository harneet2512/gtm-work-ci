// @vitest-environment jsdom
// The episode page bodies (HAR-145): rail selection is URL state; Trace, Graph diff and Raw render the
// payloads verbatim and say so when a diff does not exist.
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EpisodeRail, GraphDiffBody, RawBody, TraceBody, UnknownNodeNote } from "@/components/episode/EpisodeBody";
import { FocusNode } from "@/components/episode/FocusNode";
import type { AgentRun, GraphDiff, HumanStrategyDecision, JudgmentInference, RunStrategies, RunTrace } from "@/lib/api/types";
import { buildEpisodeView, NODE_WORD } from "@/lib/view/episode";
import { firstParam } from "@/lib/params";
import { loadFixture } from "./contract-validator";

afterEach(cleanup);

const run = loadFixture<AgentRun>("medtech.agent-run.json");
const trace = loadFixture<RunTrace>("medtech.run-trace.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const decision = loadFixture<HumanStrategyDecision>("medtech.strategy-decision.json");
const inference = loadFixture<JudgmentInference>("medtech.judgment-inference.json");
const diff = loadFixture<GraphDiff>("medtech.graph-diff.json");
const view = buildEpisodeView(run, trace, strategies, decision, inference, ["chooser"]);
const EPISODE = run.generation!.decision_episode_id!;

describe("EpisodeRail", () => {
  it("renders one link per node carrying node and mode in the URL, marking only the selected", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="trace" selected="graph" />);
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(view.nodes.length);
    expect(links[0]!.getAttribute("href")).toBe(`/episodes/${EPISODE}?node=${view.nodes[0]!.id}&mode=trace`);
    expect(document.querySelectorAll("[aria-current='true']")).toHaveLength(1);
    expect(document.querySelector("li.selected")!.textContent).toContain(view.nodes.find((n) => n.id === "graph")!.label);
  });

  it("keeps the manifest in every rail link so Message 1 stays resolvable", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="cliff" selected={null} manifest="m1" />);
    for (const a of screen.getAllByRole("link")) expect(a.getAttribute("href")).toMatch(/&manifest=m1$/);
  });

  it("gives every node's status a text word (not only a glyph) and never a check mark", () => {
    render(<EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected={null} />);
    const words = [...document.querySelectorAll("li.enode")].map((li) => li.querySelector(".sr-only")?.textContent);
    expect(words).toEqual(view.nodes.map((n) => NODE_WORD[n.status]));
    expect(document.querySelector(".episode-rail")!.textContent).not.toContain("✓");
    expect(NODE_WORD.not_observable).toBe("not observable");
    expect(NODE_WORD.absent).toBe("not recorded");
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
        <EpisodeRail nodes={view.nodes} episodeId={EPISODE} mode="story" selected="graph" />
        <FocusNode nodeId="graph" />
      </>,
    );
    const link = document.querySelector("#node-graph a") as HTMLElement;
    expect(document.activeElement).toBe(link);
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
    render(<UnknownNodeNote nodes={view.nodes} selected="graph" />);
    expect(document.body.textContent).toBe("");
    cleanup();
    render(<UnknownNodeNote nodes={view.nodes} selected={null} />);
    expect(document.body.textContent).toBe("");
  });
});

describe("TraceBody", () => {
  it("lists trigger and evidence activities and the context accesses", () => {
    const t = { ...trace, context_accesses: [{ access_id: 7, tool: "search", items: [1, 2], bytes: 99, truncated: true }, { access_id: 8, tool: "get" }] } as unknown as RunTrace;
    render(<TraceBody trace={t} />);
    const roles = [...document.querySelectorAll("table")[0]!.querySelectorAll("tbody tr td:nth-child(2)")].map((td) => td.textContent);
    expect(roles.filter((r) => r === "trigger")).toHaveLength(t.trigger_activities?.length ?? 0);
    expect(roles.filter((r) => r === "evidence")).toHaveLength(t.correlated_activities?.length ?? 0);
    const access = [...document.querySelectorAll("table")[1]!.querySelectorAll("tbody tr")].map((tr) => tr.textContent);
    expect(access[0]).toContain("search");
    expect(access[0]).toContain("yes");
    expect(access[1]).toContain("—");
    expect(access[1]).toContain("no");
  });

  it("wraps every wide table in a focusable, labelled scroll region", () => {
    const t = { ...trace, context_accesses: [{ access_id: 7, tool: "search", items: [1], bytes: 9, truncated: false }] } as unknown as RunTrace;
    render(<TraceBody trace={t} />);
    const regions = [...document.querySelectorAll(".table-scroll")];
    expect(regions.map((r) => r.getAttribute("aria-label"))).toEqual(["Trace spans table, scrollable", "Context accesses, scrollable"]);
    for (const r of regions) {
      expect(r.getAttribute("tabindex")).toBe("0");
      expect(r.classList.contains("table-scroll")).toBe(true);
      expect(r.querySelector("table")).not.toBeNull();
    }
  });

  it("omits the access table when there are no accesses and tolerates absent activity lists", () => {
    render(<TraceBody trace={{ ...trace, context_accesses: undefined, trigger_activities: undefined, correlated_activities: undefined } as unknown as RunTrace} />);
    expect(document.querySelectorAll("table")).toHaveLength(1);
    expect(document.querySelectorAll("tbody tr")).toHaveLength(0);
  });
});

describe("GraphDiffBody", () => {
  it("renders the projection diff summary and one row per change, then the state changes", () => {
    const t = { ...trace, state_diff: { changes: [{ field: "stage", before: "a", after: "b" }, {}] } } as unknown as RunTrace;
    render(<GraphDiffBody diff={diff} trace={t} />);
    expect(document.body.textContent).toContain(`+${diff.summary.added} −${diff.summary.removed} ~${diff.summary.changed} ⟲${diff.summary.repaired}`);
    const tables = document.querySelectorAll("table");
    expect(tables[0]!.querySelectorAll("tbody tr")).toHaveLength(diff.changes.length);
    const state = [...tables[1]!.querySelectorAll("tbody tr")].map((tr) => tr.textContent);
    expect(state[0]).toContain("stage");
    expect(state[0]).toContain('"a"');
    expect(state[1]).toContain("?");
    expect(state[1]).toContain("null");
  });

  it("says no projection diff was recorded and shows no state table when there are no changes", () => {
    render(<GraphDiffBody diff={null} trace={{ ...trace, state_diff: undefined } as unknown as RunTrace} />);
    expect(document.body.textContent).toContain("No graph projection diff recorded for this event.");
    expect(document.querySelectorAll("table")).toHaveLength(0);
  });

  it("falls back to the raw op for an op it has no mark for", () => {
    const odd = { ...diff, changes: [{ ...diff.changes[0]!, op: "teleported" }] } as unknown as GraphDiff;
    render(<GraphDiffBody diff={odd} trace={trace} />);
    expect(within(document.querySelector("tbody")!).getByText("teleported")).toBeTruthy();
  });
});

describe("RawBody", () => {
  it("has a collapsible per node plus the full trace, and the diff only when it exists", () => {
    const { rerender } = render(<RawBody view={view} graphDiff={diff} trace={trace} />);
    expect(document.querySelectorAll("details")).toHaveLength(view.nodes.length + 2);
    rerender(<RawBody view={view} graphDiff={null} trace={trace} />);
    expect(document.querySelectorAll("details")).toHaveLength(view.nodes.length + 1);
    expect(document.querySelector("#raw-graph pre")!.textContent).toBe(JSON.stringify(view.nodes.find((n) => n.id === "graph")!.data ?? null, null, 2));
  });
});

describe("firstParam", () => {
  it("returns a string as is, the first of an array, and undefined for nothing", () => {
    expect(firstParam("a")).toBe("a");
    expect(firstParam(["x", "y"])).toBe("x");
    expect(firstParam(undefined)).toBeUndefined();
  });
});
