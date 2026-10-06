// The fixture core's POST /eval-results/{id}/disputes must behave like the Go core (contracts/openapi/core.yaml):
// a contract-valid EvalDispute snapshotting the fixture result, identical repeats converge, and the same
// refusals. Otherwise the Playwright run would prove the form against a fake that the real core contradicts.
import { describe, expect, it } from "vitest";
import type { RunStrategies, RunTrace } from "@/lib/api/types";
import { createDisputeStore } from "./e2e/fixture-disputes.mjs";
import { loadFixture, validateSchema } from "./contract-validator";

const strategies = loadFixture<RunStrategies>("acme.run-strategies.json");
const trace = loadFixture<RunTrace>("acme.run-trace.json");
const CTA_A = "0e1a0000-0000-4000-8000-000000000910"; // candidate A, cta_calibration, fail
const CTA_B = "0e1a0000-0000-4000-8000-000000009201"; // candidate B, cta_calibration, warn
const body = { reason: "Marco asked for the documents first; a call offer is fine once they land.", expected_verdict: "pass", surface: "web", actor_label: "Dana Kim" };

describe("fixture dispute store", () => {
  it("records a contract-valid dispute that snapshots the fixture result", () => {
    const store = createDisputeStore(strategies, trace);
    const res = store.dispute(CTA_B, body);
    expect(res.status).toBe(201);
    expect(validateSchema("eval_dispute", res.body).errors).toEqual([]);
    expect(res.body).toMatchObject({ eval_result_id: CTA_B, eval_type: "cta_calibration", disputed_verdict: "warn", disputed_blocking: false, expected_verdict: "pass", draft_index: 2 });
  });

  it("returns the stored dispute for an identical repeat and a new one otherwise", () => {
    const store = createDisputeStore(strategies, trace);
    const first = store.dispute(CTA_B, body);
    const again = store.dispute(CTA_B, { ...body, reason: `  ${body.reason}  ` });
    expect(again.status).toBe(200);
    expect(again.body.id).toBe(first.body.id);
    const other = store.dispute(CTA_B, { ...body, expected_verdict: null });
    expect(other.status).toBe(201);
    expect(other.body.id).not.toBe(first.body.id);
    expect(validateSchema("eval_dispute", other.body).errors).toEqual([]);
  });

  it("refuses what the core refuses", () => {
    const store = createDisputeStore(strategies, trace);
    expect(store.dispute("99999999-9999-4999-8999-999999999999", body)).toMatchObject({ status: 404, body: { error: { code: "not_found" } } });
    expect(store.dispute(CTA_A, { ...body, expected_verdict: "fail" })).toMatchObject({ status: 422, body: { error: { code: "expected_equals_verdict" } } });
    expect(store.dispute(CTA_A, { ...body, reason: "   " })).toMatchObject({ status: 422, body: { error: { code: "invalid_request" } } });
    expect(store.dispute(CTA_A, { ...body, reason: "é".repeat(2001) })).toMatchObject({ status: 422 });
    expect(store.dispute(CTA_A, { ...body, surface: "email" })).toMatchObject({ status: 422 });
    expect(store.dispute(CTA_A, { ...body, actor_label: "" })).toMatchObject({ status: 422 });
    expect(store.dispute(CTA_A, { ...body, expected_verdict: "maybe" })).toMatchObject({ status: 422 });
    expect(store.dispute(CTA_A, { ...body, actor_person_id: "99999999-9999-4999-8999-999999999999" })).toMatchObject({ status: 422, body: { error: { code: "unknown_person" } } });
    expect(store.dispute(CTA_A, { ...body, score: 1 })).toMatchObject({ status: 400 });
    expect(store.dispute(CTA_A, null)).toMatchObject({ status: 400 });
  });

  it("disputes the results of every run it is given, and knows their people", () => {
    const medtech = { strategies: loadFixture<RunStrategies>("medtech.run-strategies.json"), trace: loadFixture<RunTrace>("medtech.run-trace.json") };
    const store = createDisputeStore(strategies, trace, [medtech]);
    const pricingC = medtech.strategies.eval_bundles[2]!.items[0]!.result!;
    const luis = medtech.trace.decisions[0]!.actor_person_id!;
    const res = store.dispute(pricingC.id, { ...body, expected_verdict: "pass", actor_person_id: luis, actor_label: "Luis Rodriguez" });
    expect(res.status).toBe(201);
    expect(res.body).toMatchObject({ eval_type: "pricing_integrity", disputed_verdict: "fail", disputed_blocking: true, draft_index: 3 });
    expect(validateSchema("eval_dispute", res.body).errors).toEqual([]);
    expect(createDisputeStore(strategies, trace).dispute(pricingC.id, body).status).toBe(404);
  });

  it("accepts a known person and a 2000-character reason", () => {
    const store = createDisputeStore(strategies, trace);
    const res = store.dispute(CTA_A, { ...body, actor_person_id: "0b0e0000-0000-4000-8000-000000000001", reason: "é".repeat(2000) });
    expect(res.status).toBe(201);
    expect(validateSchema("eval_dispute", res.body).errors).toEqual([]);
  });
});
