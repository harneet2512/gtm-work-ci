// After an edit (eval design spec 4b-3, HAR-139): show what re-evaluation changed ("CTA calibration WARN →
// PASS after your edit"), or say honestly that the edited version has not been re-evaluated; a blocking
// failure on the latest verdicts explains why Send is blocked. Never a fabricated delta.
import { describe, expect, it } from "vitest";
import type { EvalBundle, EvalResult, HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import { buildAfterEdit, editPhrases } from "@/lib/evals/after-edit";
import { buildChain } from "@/lib/view/run-chain";
import { loadExample, loadFixture } from "./contract-validator";

const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const decision = loadExample<HumanStrategyDecision>("human_strategy_decision");
const chain = buildChain(strategies);
const A = chain[0]!;
const B = chain[1]!;
const pending: HumanStrategyDecision = { ...decision, send_decision: "pending", send_decided_at: null, human_decision_id: null };

function resultLike(bundle: EvalBundle, evalType: string, verdict: EvalResult["verdict"], blocking = false): EvalResult {
  const base = bundle.items.find((i) => i.result)!.result!;
  return { ...base, id: "0e1a0000-0000-4000-8000-00000000f001", eval_type: evalType as EvalResult["eval_type"], eval_version: `${evalType}:v1`, verdict, blocking, reason: `${evalType} on the final artifact.` };
}

describe("after-edit states", () => {
  it("before anyone chooses there is nothing to re-check", () => {
    expect(buildAfterEdit(B, null, null)).toMatchObject({ state: "no_decision", deltas: [], sendBlocked: [] });
  });

  it("for an option the human did not choose, says so", () => {
    expect(buildAfterEdit(A, decision, null).state).toBe("not_chosen");
  });

  it("an unedited choice keeps the draft's verdicts as the final ones", () => {
    const view = buildAfterEdit(B, { ...decision, edits: [] }, null);
    expect(view).toMatchObject({ state: "unedited", edits: [] });
  });

  it("an edited, sent choice with no re-evaluation is not re-evaluated yet, and the open verdicts still stand", () => {
    const view = buildAfterEdit(B, decision, null);
    expect(view.state).toBe("not_reevaluated");
    expect(view.summary).toBe("Dana Kim changed the call to action and edited a paragraph.");
    expect(view.standing).toEqual([{ name: "CTA calibration", verdict: "warn" }]);
    expect(view.deltas).toEqual([]);
    expect(view.sent).toEqual({ at: "2026-09-29T16:05:00Z" });
    expect(view.sendBlocked).toEqual([]);
  });

  it("with send-time results, shows each changed verdict and counts the rest", () => {
    const reeval = { evaluatedAt: "2026-09-29T16:04:30Z", results: [resultLike(B.bundle!, "cta_calibration", "pass"), resultLike(B.bundle!, "champion_continuity", "pass"), resultLike(B.bundle!, "grounding", "fail")] };
    const view = buildAfterEdit(B, pending, reeval);
    expect(view.state).toBe("reevaluated");
    expect(view.deltas.map((d) => d.text)).toEqual(["CTA calibration WARN → PASS after your edit", "Grounding NOT CHECKED → FAIL after your edit"]);
    expect(view.unchanged).toBe(1);
    expect(view.evaluatedAt).toBe("2026-09-29T16:04:30Z");
  });

  it("explains a blocked send from the latest verdicts, only while the send is pending", () => {
    const blockedBundle = { ...A.bundle!, items: A.bundle!.items.map((i) => (i.eval_type === "champion_continuity" ? { ...i, result: { ...i.result!, blocking: true } } : i)) };
    const chosenA = { ...pending, selected_candidate_id: A.candidate.candidate_id };
    const stored = buildAfterEdit({ ...A, bundle: blockedBundle }, chosenA, null);
    expect(stored.sendBlocked).toEqual([{ name: "Relationship continuity", reason: "Marco is addressed directly while Priya is dropped from cc, breaking commercial-thread continuity." }]);
    expect(stored.sendBlockedTitle).toBe("Send is blocked: Relationship continuity still fails.");

    const repaired = buildAfterEdit({ ...A, bundle: blockedBundle }, chosenA, { evaluatedAt: "2026-09-29T16:04:30Z", results: [resultLike(A.bundle!, "champion_continuity", "pass")] });
    expect(repaired.sendBlocked).toEqual([]);
    expect(repaired.deltas.map((d) => d.text)).toEqual(["Relationship continuity FAIL → PASS after your edit"]);

    const stillBlocked = buildAfterEdit(A, chosenA, { evaluatedAt: "x", results: [resultLike(A.bundle!, "champion_continuity", "fail", true)] });
    expect(stillBlocked.sendBlocked.map((b) => b.name)).toEqual(["Relationship continuity"]);

    const sent = buildAfterEdit({ ...A, bundle: blockedBundle }, { ...chosenA, send_decision: "send", send_decided_at: "2026-09-29T16:05:00Z" }, null);
    expect(sent.sendBlocked).toEqual([]);
  });

  it("records a discard plainly", () => {
    expect(buildAfterEdit(B, { ...decision, send_decision: "discard" }, null).discarded).toBe(true);
  });
});

describe("edit phrases", () => {
  it("words every literal change kind and drops repeats", () => {
    expect(editPhrases([{ kind: "paragraph_edited" }, { kind: "paragraph_edited" }, { kind: "recipient_added" }])).toEqual(["edited a paragraph", "added a recipient"]);
    expect(editPhrases([])).toEqual([]);
  });
});
