// Knowledge view helpers (WP24): the status filter accepts only the contract's lifecycle enum,
// conditions render as English, and the history shows newest first.
import { describe, expect, it } from "vitest";
import { conditionText, countsLine, historyNewestFirst, KNOWLEDGE_STATUSES, parseKnowledgeStatus, statusBadge } from "@/lib/view/knowledge";
import type { Knowledge, KnowledgeCondition } from "@/lib/api/types";
import { loadExample, loadFixture } from "./contract-validator";

const knowledge = loadExample<Knowledge>("knowledge");
const extra = loadFixture<Knowledge>("knowledge.extra.json");

describe("parseKnowledgeStatus", () => {
  it("accepts every lifecycle status the contract enumerates", () => {
    for (const s of KNOWLEDGE_STATUSES) expect(parseKnowledgeStatus(s)).toBe(s);
  });

  it("rejects anything else — the filter is ignored before it can hit the core (which 400s)", () => {
    expect(parseKnowledgeStatus("archived")).toBeNull();
    expect(parseKnowledgeStatus("CONFIRMED")).toBeNull();
    expect(parseKnowledgeStatus("'; drop table--")).toBeNull();
    expect(parseKnowledgeStatus(undefined)).toBeNull();
    expect(parseKnowledgeStatus("")).toBeNull();
  });
});

describe("conditionText", () => {
  const c = (field: string, op: KnowledgeCondition["op"], value?: unknown): KnowledgeCondition =>
    ({ field, op, ...(value === undefined ? {} : { value }) }) as KnowledgeCondition;

  it("renders each op as a short English phrase", () => {
    expect(conditionText(c("health", "exists"))).toBe("health is present");
    expect(conditionText(c("health", "not_exists"))).toBe("health is absent");
    expect(conditionText(c("health", "is_unknown"))).toBe("health is unknown");
    expect(conditionText(c("motion", "eq", "expansion"))).toBe("motion = expansion");
    expect(conditionText(c("motion", "neq", "renewal"))).toBe("motion ≠ renewal");
    expect(conditionText(c("transition.status", "in", ["CANDIDATE", "CONFIRMED"]))).toBe("transition.status ∈ {CANDIDATE, CONFIRMED}");
    expect(conditionText(c("decision_process", "contains", "no meetings"))).toBe("decision_process contains no meetings");
    expect(conditionText(c("fields", "eq", { a: 1 }))).toBe('fields = {"a":1}');
  });
});

describe("countsLine", () => {
  it("lists non-zero evidence tallies only", () => {
    expect(countsLine(extra)).toBe("2 decisions · 1 positive");
    expect(countsLine(knowledge)).toContain(`${knowledge.counts.decisions} decisions`);
  });

  it("pluralizes counterexamples and reports a bare decision count when nothing else exists", () => {
    const bare: Knowledge = { ...knowledge, counts: { ...knowledge.counts, positive_reactions: 0, negative_reactions: 0, outcomes_advanced: 0, counterexamples: 3 } };
    expect(countsLine(bare)).toContain("3 counterexamples");
    const none: Knowledge = { ...knowledge, counts: { decisions: 1, positive_reactions: 0, negative_reactions: 0, outcomes_advanced: 0, counterexamples: 0 } };
    expect(countsLine(none)).toBe("1 decisions");
  });
});

describe("historyNewestFirst", () => {
  it("orders the lifecycle history newest first regardless of storage order", () => {
    const reversed: Knowledge = { ...extra, status_history: [...(extra.status_history ?? [])].reverse() };
    const sorted = historyNewestFirst(reversed);
    expect(sorted).toHaveLength(2);
    expect(sorted[0]!.changed_at >= sorted[sorted.length - 1]!.changed_at).toBe(true);
    expect(historyNewestFirst(extra)[0]!.to_status).toBe("provisional");
  });

  it("is empty for an object with no recorded changes", () => {
    expect(historyNewestFirst({ ...knowledge, status_history: [] })).toEqual([]);
  });
});

describe("statusBadge", () => {
  it("maps a status to its badge class verbatim", () => {
    expect(statusBadge("confirmed")).toBe("badge know-confirmed");
  });
});
