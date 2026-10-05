// The /episodes/[id] trajectory (HAR-145): one node per served TraceSpan in span order, statuses carried over as served
// (recorded / pending / not recorded), Influence always "not measured", and no node ever reads as an eval verdict.
import { describe, expect, it } from "vitest";
import type { EpisodeSummary, EpisodeTrace, TraceSpan } from "@/lib/api/types";
import { accountChangeId, buildEpisodeView, episodeHref, NODE_WORD, parseMode, sourceEventId, spanOfKind } from "@/lib/view/episode";
import { loadExample } from "./contract-validator";

const trace = loadExample<EpisodeTrace>("episode_trace");
const summary = loadExample<EpisodeSummary>("episode_summary");
const view = buildEpisodeView(summary, trace);
const byKind = (kind: string) => view.nodes.find((n) => n.kind === kind)!;

const spanOf = (kind: TraceSpan["kind"], over: Partial<TraceSpan> = {}): TraceSpan => ({ ...trace.spans.find((s) => s.kind === kind)!, ...over });
const traceOf = (spans: TraceSpan[], unassigned: string[] = []): EpisodeTrace => ({ ...trace, spans, unassigned_eval_result_ids: unassigned });

describe("buildEpisodeView", () => {
  it("emits one node per span in span order, plus the synthetic influence node", () => {
    expect(view.nodes.map((n) => n.id).filter((id) => id !== "knowledge_influence")).toEqual([...trace.spans].sort((a, b) => a.seq - b.seq).map((s) => s.id));
    expect(view.nodes).toHaveLength(trace.spans.length + 1);
  });

  it("carries each span's title, summary and served status as it is", () => {
    const precedents = byKind("precedents");
    expect(precedents.status).toBe("not_recorded");
    expect(NODE_WORD[precedents.status]).toBe("not recorded");
    const judgment = view.nodes.find((n) => n.id === "cliff_message:judgment")!;
    expect(judgment.status).toBe("pending");
    expect(NODE_WORD[judgment.status]).toBe("pending");
    expect(byKind("source_event").summary).toBe(trace.spans[0]!.summary);
    expect(byKind("source_event").label).toBe("Source event");
  });

  it("keeps retrieved, applicable and cited as three nodes and puts Influence right after cited, as not measured", () => {
    const ids = view.nodes.map((n) => n.kind);
    const at = ids.indexOf("knowledge_retrieved");
    expect(ids.slice(at, at + 4)).toEqual(["knowledge_retrieved", "knowledge_applicable", "knowledge_used", "knowledge_influence"]);
    const influence = byKind("knowledge_influence");
    expect(influence.status).toBe("not_measured");
    expect(NODE_WORD[influence.status]).toBe("not measured");
    expect(influence.summary).toContain("not measured");
    expect(influence.data).toBeNull();
  });

  it("never reports a node as passed or failed: only recorded, pending, not recorded or not measured", () => {
    for (const n of view.nodes) expect(["recorded", "pending", "not_recorded", "not_measured"]).toContain(n.status);
  });

  it("lists the rows behind a span and how many eval results judged it", () => {
    const candidates = byKind("candidates");
    expect(candidates.evalCount).toBe(2);
    expect(candidates.detail).toContain("2 eval results");
    expect(candidates.detail).toContain("+1 more");
    expect(byKind("precedents").detail).toBeNull();
    const one = buildEpisodeView(summary, traceOf([spanOf("candidates", { eval_result_ids: [trace.spans[0]!.id.replace(/.*:/, "")], refs: [] })])).nodes[0]!;
    expect(one.detail).toBe("1 eval result");
  });

  it("sorts spans by their sequence number", () => {
    const [a, b] = [spanOf("source_event", { seq: 2 }), spanOf("evidence", { seq: 1 })];
    expect(buildEpisodeView(summary, traceOf([a, b])).nodes.map((n) => n.kind)).toEqual(["evidence", "source_event"]);
  });

  it("has no nodes when the episode has no trace, and counts the results with no span", () => {
    const none = buildEpisodeView(summary, null);
    expect(none.nodes).toEqual([]);
    expect(none.unassignedEvalCount).toBe(0);
    expect(buildEpisodeView(summary, traceOf([spanOf("source_event")], ["0e000000-0000-4000-8000-000000000001", "0e000000-0000-4000-8000-000000000002"])).unassignedEvalCount).toBe(2);
  });

  it("adds no influence node when no cited-knowledge span exists", () => {
    expect(buildEpisodeView(summary, traceOf([spanOf("source_event")])).nodes.some((n) => n.kind === "knowledge_influence")).toBe(false);
  });
});

describe("span lookups", () => {
  it("finds a span by kind, the account change behind the evidence and the trigger's source event", () => {
    expect(spanOfKind(trace, "ranking")!.kind).toBe("ranking");
    expect(spanOfKind(null, "ranking")).toBeNull();
    expect(accountChangeId(trace)).toBe(trace.spans.find((s) => s.kind === "evidence")!.refs.find((r) => r.kind === "account_change")!.id);
    expect(sourceEventId(trace)).toBe(trace.spans[0]!.refs.find((r) => r.kind === "source_event")!.id);
  });

  it("is null when the span or its ref is missing", () => {
    expect(accountChangeId(traceOf([spanOf("evidence", { refs: [] })]))).toBeNull();
    expect(sourceEventId(traceOf([spanOf("evidence")]))).toBeNull();
    expect(accountChangeId(null)).toBeNull();
    expect(sourceEventId(null)).toBeNull();
  });
});

describe("parseMode", () => {
  it("defaults to story and accepts the five modes", () => {
    expect(parseMode(undefined)).toBe("story");
    expect(parseMode("nonsense")).toBe("story");
    for (const m of ["story", "trace", "graphdiff", "cliff", "raw"] as const) expect(parseMode(m)).toBe(m);
  });
});

describe("episodeHref", () => {
  it("builds the bare path, and keeps node, mode and manifest in a stable order", () => {
    expect(episodeHref("e1")).toBe("/episodes/e1");
    expect(episodeHref("e1", { node: "graph", mode: "trace" })).toBe("/episodes/e1?node=graph&mode=trace");
    expect(episodeHref("e1", { mode: "cliff", manifest: "m1" })).toBe("/episodes/e1?mode=cliff&manifest=m1");
    expect(episodeHref("e1", { manifest: null, node: null })).toBe("/episodes/e1");
  });

  it("encodes a span id (it contains a colon) as URL state", () => {
    expect(episodeHref("e1", { node: "knowledge_used:0" })).toBe("/episodes/e1?node=knowledge_used%3A0");
  });
});
