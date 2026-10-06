// @vitest-environment jsdom
// The inspector's loaders (the registry, the fleet, one episode) against a fake core, and the views that render what they load.
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DecisionView } from "@/components/evals/inspector/DecisionView";
import { EpisodePathView } from "@/components/evals/inspector/EpisodePathView";
import { EpisodePicker } from "@/components/evals/inspector/EpisodePicker";
import { InspectorNav } from "@/components/evals/inspector/InspectorNav";
import { inputFromEvidence } from "@/lib/evals/inspector/inputs";
import { loadEpisodeInspector } from "@/lib/evals/inspector/load-episode-inspector";
import { loadFleet, loadHealthEpisodes } from "@/lib/evals/inspector/load-fleet";
import { loadInspectorRegistry } from "@/lib/evals/inspector/load-registry";
import { isTestData, TEST_DATA_TITLE } from "@/lib/evals/inspector/test-data";
import { CAND_A, CAND_B, CAND_C, EP, result } from "./fixtures";

afterEach(() => {
  cleanup();
  vi.unstubAllEnvs();
});

const ACT = "0e7a1000-0000-4000-8000-0000000000a1";
const KNOW = "0e7a1000-0000-4000-8000-0000000000b1";

const cand = (id: string, rank: number, title: string) => ({
  candidate_id: id, strategy_type: "stronger_cta", title, description: `${title} intent`, ranking: rank, preferred_by_agent: rank === 1, rationale: "r",
  state_refs: ["blockers"], evidence_refs: [{ activity_id: ACT }], knowledge_refs: [KNOW], action_type: "send_email", action_class: "REPLY", to: [], cc: [], subject: "s",
  five_questions: {}, preview: "p",
});

const summary = {
  id: EP, agent_run_id: "0e7a1000-0000-4000-8000-0000000000aa", account_id: "0e7a1000-0000-4000-8000-0000000000ac", account_name: "MedTech Advances",
  status: "decided", final_status: "send_recorded", run: { id: "r", phase: "published", status: "succeeded", state_version: 1, model: null },
  triggering_event: { activity_id: ACT, summary: "Fatoumata asked about costs.", occurred_at: "2023-11-09T09:00:00Z" },
  recommended_action: { candidate_id: CAND_A, ranking: 1 }, selected_action: { candidate_id: CAND_B, ranking: 2 },
  human_outcome: { agreement: "overrode", human_action: "APPROVE_WITH_EDIT", send_decision: "send", edited: true },
};

function api(over: Record<string, unknown> = {}) {
  return {
    listEvalRunsPage: vi.fn(async () => ({ items: [{ decision_episode_id: EP }, { decision_episode_id: EP }, { decision_episode_id: null }], nextCursor: null })),
    getEpisode: vi.fn(async () => summary),
    getEpisodeTrace: vi.fn(async () => ({ spans: [{ id: `candidates:${EP}`, kind: "candidates", title: "Candidates", refs: [], seq: 1, occurred_at: null }] })),
    listEpisodeGateResults: vi.fn(async () => [result({ gate: "D3", sub_gate: "ranking", verdict: "warn", criteria: [{ id: "uncertainty_reflected", label: "", result: "warn", why: "w", evidence_refs: ["activity:a"] }] }), result({ gate: "D2", sub_gate: "candidate", judged_object: { type: "StrategyCandidate", id: CAND_A } })]),
    getTimeline: vi.fn(async () => [{ id: ACT, summary: "Fatoumata asked about costs.", activity_type: "email_received", occurred_at: "2023-11-09T09:00:00Z" }]),
    getRunStrategies: vi.fn(async () => ({ strategy_set: { id: "s", candidates: [cand(CAND_A, 1, "A title"), cand(CAND_B, 2, "B title"), cand(CAND_C, 3, "C title")] }, eval_bundles: [] })),
    getStrategyDecision: vi.fn(async () => ({ edits: [{ kind: "paragraph_edited" }] })),
    getJudgmentInference: vi.fn(async () => ({ inferred_semantic_delta: { statement: "Softer ask.", confidence: 0.6, edit_class: ["cta"] }, agreement: "overrode" })),
    getRunRecomputation: vi.fn(async () => ({ status: "reevaluated", entries: [{ index: 0, edit: { field: "body", kind: "paragraph_edited" }, invalidated: [], recomputed: [{ kind: "final_artifact", label: "Final artifact", reason: "r", verdict: null, ref_id: null, field: null, replaces: null }], not_recomputed: [], preserved: [] }], semantic_labels: [], account_state: { preserved: true, version_before: 1, version_after: 1 }, preserved_overall: [] })),
    getEpisodeRanking: vi.fn(async () => ({ order: [CAND_A, CAND_B, CAND_C], reasons: [{ ranked_higher_id: CAND_A, ranked_lower_id: CAND_B, reason: "fits", evidence_refs: [], knowledge_refs: [] }], tier_inputs: [], abstained: false })),
    getKnowledge: vi.fn(async () => ({ id: KNOW, title: "Legal gates the close" })),
    getEpisodeMetrics: vi.fn(async () => ({ measured: false, usage_source: null, stages: [] })),
    ...over,
  };
}

describe("loaders", () => {
  it("the registry loads the 25 gates with plain words and the control effect rules", () => {
    const r = loadInspectorRegistry();
    expect(r.defs).toHaveLength(25);
    expect(r.rules?.vocabulary).toContain("RECORD ONLY");
    expect(r.deviations.map((d) => d.id)).toContain("single_trial");
  });

  it("the fleet lists each episode once with its stored results; a failed read is dropped and counted", async () => {
    const fleet = await loadFleet(api() as never);
    expect(fleet.status).toBe("ok");
    expect(fleet.episodes).toHaveLength(1);
    expect(fleet.episodes[0]!.label).toBe("MedTech Advances · Nov 9, 2023");
    const broken = await loadFleet(api({ getEpisode: vi.fn(async () => { throw new Error("down"); }) }) as never);
    expect(broken.status).toBe("unavailable");
    expect(broken.skipped).toBe(1);
    const gone = await loadFleet(api({ listEvalRunsPage: vi.fn(async () => { throw new Error("down"); }) }) as never);
    expect(gone).toEqual({ status: "unavailable", episodes: [], skipped: 0 });
    const missing = await loadFleet(api({ getEpisode: vi.fn(async () => null) }) as never);
    expect(missing.episodes).toEqual([]);
  });

  it("pages through the eval runs until the last page", async () => {
    const pages = [{ items: [{ decision_episode_id: "e1" }], nextCursor: "c2" }, { items: [{ decision_episode_id: "e2" }], nextCursor: null }];
    const listEvalRunsPage = vi.fn(async () => pages.shift()!);
    const fleet = await loadFleet(api({ listEvalRunsPage }) as never);
    expect(listEvalRunsPage).toHaveBeenCalledTimes(2);
    expect(fleet.episodes).toHaveLength(2);
  });

  it("health episodes carry metrics and the trace's span kinds; an unreadable trace is null, not empty", async () => {
    const ok = await loadHealthEpisodes(api() as never);
    expect(ok.episodes[0]!.spanKinds).toEqual(["candidates"]);
    const noTrace = await loadHealthEpisodes(api({ getEpisodeTrace: vi.fn(async () => { throw new Error("x"); }) }) as never);
    expect(noTrace.episodes[0]!.spanKinds).toBeNull();
    expect((await loadHealthEpisodes(api({ listEvalRunsPage: vi.fn(async () => { throw new Error("x"); }) }) as never)).status).toBe("unavailable");
    expect((await loadHealthEpisodes(api({ getEpisode: vi.fn(async () => null) }) as never)).episodes).toEqual([]);
  });

  it("an episode inspector builds the path, a drawer per gate and the decision screen", async () => {
    const reg = loadInspectorRegistry();
    const fleet = (await loadFleet(api() as never)).episodes;
    const ep = await loadEpisodeInspector(api() as never, EP, reg, fleet);
    expect(ep).not.toBeNull();
    expect(ep!.statusLine).toMatch(/dry run/);
    expect(ep!.eventSummary).toBe("Fatoumata asked about costs.");
    expect(Object.keys(ep!.drawers)).toHaveLength(19);
    expect(ep!.screen.cards.map((c) => c.title)).toEqual(["A title", "B title", "C title"]);
    expect(ep!.screen.cards[0]!.knowledge[0]!.title).toBe("Legal gates the close");
    const d3 = ep!.drawers.D3!;
    expect(d3.inputs.items[0]!.kind).toBe("ranking");
    expect(ep!.drawers.D5!.happened.confidence).toBe(0.6);
    const edit = ep!.path.items.find((i) => i.type === "edit");
    expect(edit && edit.type === "edit" && edit.text).toBe("Softer ask.");
    expect(edit && edit.type === "edit" && edit.recomputed).toEqual(["Final artifact"]);
    expect(await loadEpisodeInspector(api({ getEpisode: vi.fn(async () => null) }) as never, EP, reg, [])).toBeNull();
  });

  it("every other read degrades to a notice; the page still renders what exists", async () => {
    const fail = vi.fn(async () => { throw new Error("down"); });
    const reg = loadInspectorRegistry();
    const ep = await loadEpisodeInspector(api({ getEpisodeTrace: fail, listEpisodeGateResults: fail, getTimeline: fail, getRunStrategies: fail, getStrategyDecision: fail, getJudgmentInference: fail, getRunRecomputation: fail, getEpisodeRanking: fail }) as never, EP, reg, []);
    expect(ep!.notices).toHaveLength(8);
    expect(ep!.screen.cards).toEqual([]);
    expect(ep!.path.counts.ran).toBe(0);
    const noKnowledge = await loadEpisodeInspector(api({ getKnowledge: fail, getJudgmentInference: vi.fn(async () => null), getStrategyDecision: vi.fn(async () => null) }) as never, EP, reg, []);
    expect(noKnowledge!.screen.cards[0]!.knowledge[0]!.title).toBeNull();
    const edit = noKnowledge!.path.items.find((i) => i.type === "edit");
    expect(edit && edit.type === "edit" && edit.text).toMatch(/edited the draft/);
  });
});

describe("test data", () => {
  it("is on only when GTM_TEST_DATA is 1: titles say so and the badge shows", async () => {
    expect(isTestData({})).toBe(false);
    vi.stubEnv("GTM_TEST_DATA", "1");
    expect(isTestData()).toBe(true);
    const fleet = await loadFleet(api() as never);
    expect(fleet.episodes[0]!.label).toBe(`${TEST_DATA_TITLE} · Nov 9, 2023`);
    render(<InspectorNav active="loop" demo={false} episodeId={null} />);
    expect(screen.getByTestId("test-data-badge").textContent).toBe("TEST DATA");
  });
});

describe("views", () => {
  it("the nav marks the active tab, carries the episode and hides the operator link in Demo mode", () => {
    const { unmount } = render(<InspectorNav active="episode" demo={false} episodeId={EP} />);
    expect(screen.getByRole("link", { name: "The three options" }).getAttribute("href")).toBe(`/evals/episode/${EP}/decision`);
    expect(screen.getByRole("link", { name: "All gate results" })).toBeTruthy();
    expect(screen.queryByTestId("test-data-badge")).toBeNull();
    unmount();
    render(<InspectorNav active="decision" demo episodeId={null} />);
    expect(screen.getByRole("link", { name: "Live episode" }).getAttribute("href")).toBe("/evals/episode?demo=1");
    expect(screen.getByRole("link", { name: "Offline checks" }).getAttribute("href")).toBe("/evals/offline?demo=1");
    expect(screen.queryByRole("link", { name: "All gate results" })).toBeNull();
  });

  it("the picker lists episodes by their event; none is said plainly", () => {
    const s = { ...summary, triggering_event: summary.triggering_event } as never;
    const { unmount } = render(<EpisodePicker demo={false} episodes={[{ episodeId: EP, label: "x", summary: s, resultCount: 1 }]} />);
    expect(screen.getByText("MedTech Advances")).toBeTruthy();
    expect(screen.getByText(/1 stored result$/)).toBeTruthy();
    unmount();
    render(<EpisodePicker demo={false} episodes={[]} />);
    expect(screen.getByText(/No episode has been evaluated yet/)).toBeTruthy();
    cleanup();
    const bare = { ...summary, triggering_event: null } as never;
    render(<EpisodePicker demo episodes={[{ episodeId: EP, label: "x", summary: bare, resultCount: 2 }]} />);
    expect(screen.getByText(/no recorded summary/)).toBeTruthy();
    expect(screen.getByText(/2 stored results/)).toBeTruthy();
  });

  it("inputs name an option by its title and keep an unresolved record as a plain label", () => {
    const ev = (ref: string, summary: string | null = null) => ({ ref, kind: ref.split(":")[0]!, id: ref.split(":")[1]!, label: `L ${ref}`, summary, spanId: null, href: null, source: null });
    expect(inputFromEvidence(ev("candidate:c1"), () => "Option title").title).toBe("Option title");
    expect(inputFromEvidence(ev("knowledge:k1"), () => null).kind).toBe("knowledge");
    expect(inputFromEvidence(ev("human_strategy_decision:h"), () => null).kind).toBe("human");
    expect(inputFromEvidence(ev("account_state:a"), () => null).kind).toBe("state");
    expect(inputFromEvidence(ev("agent_run_step:s"), () => null).kind).toBe("trace");
    expect(inputFromEvidence(ev("effect:e"), () => null).kind).toBe("other");
    expect(inputFromEvidence({ ...ev("activity:a", "text"), source: "Email · Nov 9" }, () => null).title).toBe("Email · Nov 9");
  });
});

describe("the episode path and decision views on loaded data", () => {
  async function loaded() {
    return (await loadEpisodeInspector(api() as never, EP, loadInspectorRegistry(), (await loadFleet(api() as never)).episodes))!;
  }

  it("opens a drawer on click and closes it; ?gate= opens one on load; the edit says what was re-derived", async () => {
    const ep = await loaded();
    const { unmount } = render(<EpisodePathView episode={ep} demo={false} initialGate={null} />);
    expect(screen.getByTestId("path-edit").textContent).toMatch(/Softer ask/);
    expect(screen.getByTestId("path-edit").textContent).toMatch(/Final artifact/);
    expect(screen.queryByTestId("eval-drawer")).toBeNull();
    fireEvent.click(document.querySelector(".path-node[data-gate='D3']") as HTMLElement);
    expect(screen.getByTestId("eval-drawer").getAttribute("data-gate")).toBe("D3");
    fireEvent.click(document.querySelector(".path-node[data-gate='D3']") as HTMLElement);
    expect(screen.queryByTestId("eval-drawer")).toBeNull();
    expect(screen.getByRole("link", { name: "All gate results" })).toBeTruthy();
    unmount();
    render(<EpisodePathView episode={{ ...ep, notices: ["The stored ranking could not be read right now."] }} demo initialGate="D2" />);
    expect(screen.getByTestId("eval-drawer").getAttribute("data-gate")).toBe("D2");
    expect(screen.getByRole("status").textContent).toMatch(/could not be read/);
    expect(screen.queryByRole("link", { name: "All gate results" })).toBeNull();
  });

  it("an edit with no recompute record says so", async () => {
    const ep = await loadEpisodeInspector(api({ getRunRecomputation: vi.fn(async () => null) }) as never, EP, loadInspectorRegistry(), []);
    render(<EpisodePathView episode={ep!} demo={false} initialGate="not-a-gate" />);
    expect(screen.getByTestId("path-edit").textContent).toMatch(/No recompute record is stored/);
    expect(screen.queryByTestId("eval-drawer")).toBeNull();
  });

  it("the decision screen shows options, why A won, the human's choice and the edit", async () => {
    const ep = await loaded();
    render(<DecisionView screen={ep.screen} />);
    expect(screen.getAllByText(/^Option [ABC]$/)).toHaveLength(3);
    expect(screen.getByText("Why A won")).toBeTruthy();
    expect(screen.getByText("The human chose differently")).toBeTruthy();
    expect(screen.getByText("What the edit changed")).toBeTruthy();
    expect(screen.getByText(/Final artifact/)).toBeTruthy();
    expect(screen.getByText(/verdicts, not scores/)).toBeTruthy();
  });

  it("the decision screen without a ranking, a human or a D2 result says what is missing", async () => {
    const ep = await loadEpisodeInspector(
      api({
        getEpisodeRanking: vi.fn(async () => null),
        getRunRecomputation: vi.fn(async () => null),
        getJudgmentInference: vi.fn(async () => null),
        listEpisodeGateResults: vi.fn(async () => []),
      }) as never,
      EP,
      loadInspectorRegistry(),
      [],
    );
    render(<DecisionView screen={ep!.screen} />);
    expect(screen.getByText(/ranking rationale is not stored/)).toBeTruthy();
    expect(screen.getByText(/The ranking itself has not been judged/)).toBeTruthy();
    expect(screen.getByText(/not available for this episode/)).toBeTruthy();
    expect(screen.getByText(/The final message has not been checked/)).toBeTruthy();
    expect(screen.getAllByText(/has not been judged: no D2 result/).length).toBe(3);
  });
});
