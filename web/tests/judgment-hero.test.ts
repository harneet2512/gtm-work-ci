// The Judgment Episode hero model: every value comes from existing reads, a missing piece says why, nothing is invented.
import { describe, expect, it } from "vitest";
import { buildHero, DEMO_HINT, type HeroInput } from "@/lib/view/judgment-hero";

const cand = (id: string, ranking: number, title: string, preferred = false) => ({ candidate_id: id, ranking, title, preferred_by_agent: preferred });
const strategies = (cands: ReturnType<typeof cand>[]) => ({ strategy_set: { candidates: cands } }) as never;
const three = strategies([cand("c1", 1, "Reply with the security pack", true), cand("c2", 2, "Offer a call"), cand("c3", 3, "Wait a week")]);

const base: HeroInput = {
  summary: { triggering_event: { summary: "Buyer asked about SOC 2" }, final_status: "awaiting_choice", selected_action: null } as never,
  bi: null,
  strategies: null,
  decision: null,
  inference: null,
  recomputation: null,
  mutations: null,
  gateRows: [],
};
const full: HeroInput = {
  summary: { triggering_event: { summary: "Buyer asked about SOC 2" }, final_status: "sent", selected_action: { candidate_id: "c2", ranking: 2, title: "Offer a call", strategy_type: "x", action_type: "send_email", preferred_by_agent: false } } as never,
  bi: { summary: "The buyer now needs a security review before signing." } as never,
  strategies: three,
  decision: { selected_candidate_id: "c2", original_agent_preference: "c1", edits: [{ kind: "cta_changed", before: "Book 30 minutes", after: "Book 15 minutes" }], send_decision: "send" } as never,
  inference: { inferred_semantic_delta: { statement: "Smaller ask", edit_class: ["cta"], signal_strength: "moderate" } } as never,
  recomputation: { status: "reevaluated", entries: [{ invalidated: [{ label: "CTA" }], recomputed: [{ label: "CTA eval" }], not_recomputed: [], preserved: [{ label: "Recipients" }, { label: "Subject" }] }], preserved_overall: [{ label: "Account state" }] } as never,
  mutations: [{ operation: "CREATE", title: "Offer a short call before a security pack", scope: "account_specific", status: "candidate", evidence: { kind: "human_decision" } }] as never,
  gateRows: [{ gate: "D6", label: "MISSING_CRITERION", measured: true }] as never,
};

describe("buildHero", () => {
  it("shows the three messages with their section titles", () => {
    const h = buildHero(full);
    expect(h.messages.map((m) => m.title)).toEqual(["Message 1 — what changed", "Message 2 — gtm_ai recommendation → human judgment → judgment delta → recompute → final action", "Message 3 — eval gap → judgment learning"]);
  });
  it("reads what changed from Message 1's update", () => {
    expect(buildHero(full).messages[0]!.fields[0]!.lines).toEqual(["The buyer now needs a security review before signing."]);
  });
  it("shows the recommendation as A with alternatives B and C in ranking order", () => {
    const f = buildHero(full).messages[1]!.fields[0]!;
    expect(f.lines).toEqual(["A · Reply with the security pack", "Alternatives: B · Offer a call; C · Wait a week"]);
  });
  it("states the human judgment as selected B instead of A, without calling it correct", () => {
    const f = buildHero(full).messages[1]!.fields[1]!;
    expect(f.lines).toEqual(["Selected B instead of A"]);
    expect(JSON.stringify(buildHero(full))).not.toMatch(/correct choice|was right/i);
  });
  it("shows the delta class, before to after and signal strength", () => {
    const f = buildHero(full).messages[1]!.fields[2]!;
    expect(f.label).toBe("Judgment Delta");
    expect(f.delta).toMatchObject({ chip: "cta", statement: "Smaller ask", strength: "moderate" });
    expect(f.delta!.diff!.segments.filter((s) => s.kind !== "same").map((s) => s.text.trim())).toEqual(["30", "15"]);
    expect(f.lines).toEqual(["cta", "Smaller ask", "Signal strength: moderate"]);
  });
  it("counts what the recompute touched against what stayed stable", () => {
    expect(buildHero(full).messages[1]!.fields[3]!.lines).toEqual(["Judgment Recompute: 2 affected · 3 stable"]);
  });
  it("states the final action", () => {
    expect(buildHero(full).messages[1]!.fields[4]!.lines).toEqual(["Sent: B · Offer a call"]);
  });
  it("shows the eval gap outcome label", () => {
    expect(buildHero(full).messages[2]!.fields[0]!.lines).toEqual(["Missing judgment criterion"]);
  });
  it("calls a candidate rule a candidate, never learned, and carries scope, evidence and status", () => {
    const f = buildHero(full).messages[2]!.fields[1]!;
    expect(f.label).toBe("Judgment Learning");
    expect(f.lines).toEqual(["Candidate Judgment Rule: Offer a short call before a security pack", "Scope: account specific", "Evidence: human decision", "Status: candidate"]);
    expect(f.lines.join(" ")).not.toMatch(/learned/i);
  });
  it("only says learned once the status is supported or confirmed", () => {
    const confirmed = { ...full, mutations: [{ ...(full.mutations as never as object[])[0]!, status: "confirmed" }] as never };
    expect(buildHero(confirmed).messages[2]!.fields[1]!.lines[0]).toBe("Learned: Offer a short call before a security pack");
  });
  it("never says Ghost", () => {
    expect(JSON.stringify(buildHero(full))).not.toMatch(/Ghost/);
  });
});

describe("missing pieces", () => {
  it("say Not run yet for something that has not happened, and never fabricate", () => {
    const h = buildHero(base);
    const all = h.messages.flatMap((m) => m.fields);
    expect(all.every((f) => f.state === "empty")).toBe(false); // the trigger event still gives "what changed"
    expect(h.messages[1]!.fields[1]!.lines).toEqual(["Not run yet — no human choice has been recorded"]);
    expect(h.messages[1]!.fields[0]!.lines).toEqual(["Not run yet — no strategy set was read"]);
    expect(h.messages[2]!.fields[1]!.lines).toEqual(["Not run yet"]);
  });
  it("says Not classified yet, not unclassified, when the edit has no inference", () => {
    const f = buildHero({ ...full, inference: null }).messages[1]!.fields[2]!;
    expect(f.delta!.chip).toBe("Not classified yet");
    expect(f.delta!.statement).toBeNull();
  });
  it("says Not applicable with the reason when no edit was made", () => {
    const noEdit: HeroInput = { ...full, decision: { ...(full.decision as object), edits: [] } as never, inference: null, recomputation: { status: "unedited", entries: [], preserved_overall: [] } as never, gateRows: [], mutations: [] };
    const h = buildHero(noEdit);
    expect(h.messages[1]!.fields[2]!.lines).toEqual(["Not applicable — the human made no edit"]);
    expect(h.messages[1]!.fields[3]!.lines).toEqual(["Not applicable — nothing was edited, so nothing was recomputed"]);
    expect(h.messages[2]!.fields[0]!.lines).toEqual(["Not applicable — no human edit to compare against the evals"]);
    expect(h.messages[2]!.fields[1]!.lines).toEqual(["Not applicable — this episode did not change company knowledge"]);
  });
  it("shows the agreed case without implying an override", () => {
    const agreed: HeroInput = { ...full, decision: { ...(full.decision as object), selected_candidate_id: "c1" } as never };
    expect(buildHero(agreed).messages[1]!.fields[1]!.lines).toEqual(["Selected A, as recommended"]);
  });
});

describe("presenter hint", () => {
  it("is HAR-145's demo sentence", () => {
    expect(DEMO_HINT).toContain("gtm_ai");
    expect(buildHero(full).demoHint).toBe(DEMO_HINT);
  });
});
