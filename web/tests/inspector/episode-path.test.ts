import { describe, expect, it } from "vitest";
import type { EpisodeSummary } from "@/lib/api/types";
import { buildEpisodePath, PATH_ORDER } from "@/lib/evals/inspector/episode-path";
import { CAND_A, CAND_B, def, EP, result, RULES } from "./fixtures";

const DEFS = PATH_ORDER.map((g) => def(g, { mode: ["B5", "B6", "B7", "B9", "D4", "D5", "D6", "D7", "D10"].includes(g) ? "live_conditional" : "live_required" }));

function summary(over: Partial<EpisodeSummary> = {}): EpisodeSummary {
  return {
    id: EP,
    agent_run_id: "0e7a1000-0000-4000-8000-0000000000aa",
    account_id: "0e7a1000-0000-4000-8000-0000000000ac",
    account_name: "MedTech",
    opportunity_id: null,
    strategy_set_id: null,
    status: "decided",
    final_status: "sent",
    triggering_event: null,
    run: { id: "r", status: "succeeded", phase: "published", state_version: 3, model: "m" },
    recommended_action: null,
    selected_action: null,
    human_outcome: { agreement: "agreed", human_action: "APPROVE_UNCHANGED", send_decision: "send", edited: false, actor_label: "a", chosen_at: "2026-01-01T00:00:00Z", send_decided_at: "2026-01-01T00:01:00Z" },
    judgment_status: "none",
    replay: null,
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  } as EpisodeSummary;
}

const node = (p: ReturnType<typeof buildEpisodePath>, gate: string) => {
  const n = p.items.find((i) => i.type === "gate" && i.gate === gate);
  if (!n || n.type !== "gate") throw new Error(`no node ${gate}`);
  return n;
};

describe("episode path (HAR-149 section 2)", () => {
  it("lists the B and D gates in causal order and leaves out the system gates", () => {
    const p = buildEpisodePath({ summary: summary(), results: [], defs: DEFS, rules: RULES });
    const gates = p.items.filter((i) => i.type === "gate").map((i) => (i.type === "gate" ? i.gate : ""));
    expect(gates).toEqual(PATH_ORDER);
    expect(gates.some((g) => g.startsWith("S"))).toBe(false);
    expect(gates.indexOf("D7")).toBeLessThan(gates.indexOf("D8"));
    expect(gates.indexOf("D8")).toBeLessThan(gates.indexOf("D9"));
  });

  it("D6 follows D5 and precedes D7: the check of what the human corrected comes right after the edit is understood", () => {
    expect(PATH_ORDER.indexOf("D5")).toBeLessThan(PATH_ORDER.indexOf("D6"));
    expect(PATH_ORDER.indexOf("D6")).toBeLessThan(PATH_ORDER.indexOf("D7"));
    expect(PATH_ORDER.indexOf("D9")).toBeLessThan(PATH_ORDER.indexOf("D10"));
  });

  it("not applicable comes from a stored not_applicable criterion, never from the prose", () => {
    const na = { id: "precedent_retrieved", label: "", result: "not_applicable" as const, why: "retrieval returned no earlier case", evidence_refs: [] };
    const stored = buildEpisodePath({ summary: summary(), results: [result({ gate: "B5", verdict: "unknown", observed: "0 precedents", criteria: [na] })], defs: DEFS, rules: RULES });
    expect(node(stored, "B5").status).toBe("not_applicable");
    expect(node(stored, "B5").verdict).toBeNull();
    // the same words with nothing stored read as the stored unknown, honestly
    const prose = buildEpisodePath({ summary: summary(), results: [result({ gate: "B5", verdict: "unknown", observed: "0 precedents", why: "no precedent source", criteria: [] })], defs: DEFS, rules: RULES });
    expect(node(prose, "B5").status).toBe("ran");
    expect(node(prose, "B5").statusLabel).toMatch(/could not tell/i);
    // a stored verdict other than unknown is never hidden behind not applicable
    const verdictWins = buildEpisodePath({ summary: summary(), results: [result({ gate: "B5", verdict: "fail", criteria: [na] })], defs: DEFS, rules: RULES });
    expect(node(verdictWins, "B5").status).toBe("failed");
    // a stored not_applicable next to a real FAIL is never hidden
    const mixed = buildEpisodePath({ summary: summary(), results: [result({ gate: "B5", verdict: "fail", criteria: [na, { ...na, id: "other", result: "fail" as const }] })], defs: DEFS, rules: RULES });
    expect(node(mixed, "B5").status).toBe("failed");
  });

  it("each node leads with the human question, not the id", () => {
    const n = node(buildEpisodePath({ summary: summary(), results: [], defs: DEFS, rules: RULES }), "B5");
    expect(n.question).toBe("Is B5 right?");
    expect(n.question).not.toMatch(/^B5$/);
  });

  it("a stored pass reads passed, a fail reads failed, a warning reads ran with its verdict", () => {
    const p = buildEpisodePath({
      summary: summary(),
      results: [result({ gate: "B1", verdict: "pass" }), result({ gate: "B2", verdict: "fail" }), result({ gate: "B3", verdict: "warn" }), result({ gate: "B4", verdict: "unknown", evidence_refs: [] })],
      defs: DEFS,
      rules: RULES,
    });
    expect(node(p, "B1").status).toBe("passed");
    expect(node(p, "B2").status).toBe("failed");
    expect(node(p, "B3").status).toBe("ran");
    expect(node(p, "B3").verdict).toBe("warn");
    expect(node(p, "B4").status).toBe("ran");
    expect(node(p, "B4").statusLabel).toMatch(/could not tell/i);
  });

  it("a conditional gate with no result is waiting before its trigger and not applicable when the trigger will not happen", () => {
    const waiting = buildEpisodePath({ summary: summary({ human_outcome: null, final_status: "awaiting_choice" }), results: [], defs: DEFS, rules: RULES });
    expect(node(waiting, "D5").status).toBe("waiting");
    expect(node(waiting, "D4").status).toBe("waiting");
    const unedited = buildEpisodePath({ summary: summary(), results: [], defs: DEFS, rules: RULES });
    expect(node(unedited, "D5").status).toBe("not_applicable");
    expect(node(unedited, "D7").status).toBe("not_applicable");
    expect(node(unedited, "D5").statusLabel).toMatch(/no human edit/i);
  });

  it("a gate whose trigger happened but stored nothing says so; it is never shown as passed", () => {
    const p = buildEpisodePath({ summary: summary(), results: [], defs: DEFS, rules: RULES });
    expect(node(p, "B1").status).toBe("not_recorded");
    expect(node(p, "D9").status).toBe("not_recorded");
  });

  it("a recomputed or retried result says so", () => {
    const p = buildEpisodePath({
      summary: summary(),
      results: [result({ gate: "D8", lineage: { recompute_of: "x", previous_verdict: "fail" } }), result({ gate: "D2", lineage: { retry_of: "y" } })],
      defs: DEFS,
      rules: RULES,
    });
    expect(node(p, "D8").status).toBe("recomputed");
    expect(node(p, "D8").statusLabel).toBe("Recomputed: passed");
    expect(node(p, "D8").effect?.headline).toBe("RECOMPUTE");
    expect(node(p, "D2").status).toBe("retried");
  });

  it("the ranking is overridden when the human chose a different option", () => {
    const s = summary({ human_outcome: { ...summary().human_outcome!, agreement: "overrode" } });
    const p = buildEpisodePath({ summary: s, results: [result({ gate: "D3", verdict: "pass" })], defs: DEFS, rules: RULES });
    expect(node(p, "D3").status).toBe("overridden");
    expect(node(p, "D3").statusLabel).toMatch(/overridden/i);
  });

  it("the human edit appears inline after the choice, listing what the recompute record says it re-derived", () => {
    const s = summary({ human_outcome: { ...summary().human_outcome!, edited: true, human_action: "APPROVE_WITH_EDIT" } });
    const p = buildEpisodePath({
      summary: s,
      results: [result({ gate: "D5" }), result({ gate: "D7" }), result({ gate: "D8" })],
      defs: DEFS,
      rules: RULES,
      editText: "Moved the ask from Friday to next week.",
      recomputation: { entries: [{ recomputed: [{ label: "Final artifact as sent" }, { label: "grounding" }] }, { recomputed: [{ label: "grounding" }] }] } as never,
    });
    const idx = p.items.findIndex((i) => i.type === "edit");
    expect(idx).toBeGreaterThan(-1);
    const edit = p.items[idx]!;
    if (edit.type !== "edit") throw new Error("not an edit");
    expect(edit.text).toBe("Moved the ask from Friday to next week.");
    expect(edit.recomputed).toEqual(["Final artifact as sent", "grounding"]);
    const before = p.items.slice(0, idx).map((i) => (i.type === "gate" ? i.gate : ""));
    expect(before).toContain("D4");
    expect(before).not.toContain("D5");
    expect(before).not.toContain("D7");
  });

  it("with no recompute record the edit says nothing was re-run: D7 and D8 are not assumed", () => {
    const s = summary({ human_outcome: { ...summary().human_outcome!, edited: true } });
    const p = buildEpisodePath({ summary: s, results: [result({ gate: "D7" }), result({ gate: "D8" })], defs: DEFS, rules: RULES });
    const edit = p.items.find((i) => i.type === "edit");
    if (!edit || edit.type !== "edit") throw new Error("no edit");
    expect(edit.recomputed).toEqual([]);
    expect(node(p, "D8").status).toBe("passed");
  });

  it("no edit item when the human did not edit", () => {
    const p = buildEpisodePath({ summary: summary(), results: [], defs: DEFS, rules: RULES });
    expect(p.items.some((i) => i.type === "edit")).toBe(false);
  });

  it("counts what ran so the header can say how much of the episode is measured", () => {
    const p = buildEpisodePath({ summary: summary(), results: [result({ gate: "B1" }), result({ gate: "B2", verdict: "fail" })], defs: DEFS, rules: RULES });
    expect(p.counts.ran).toBe(2);
    expect(p.counts.failed).toBe(1);
    expect(p.counts.total).toBe(PATH_ORDER.length);
  });
});

void CAND_A;
void CAND_B;
