// POST /eval-results/{id}/disputes from the web ("This eval is wrong", HAR-97 E19): the body the contract
// defines, the stored dispute back, and the core's error codes surfaced as CoreError.
import { describe, expect, it, vi } from "vitest";
import { CoreError, createCoreClient, InvalidIdError } from "@/lib/api/core-client";
import type { EvalDispute } from "@/lib/api/types";
import { loadExample, validateComponent } from "./contract-validator";

const RESULT = "0e1a0000-0000-4000-8000-000000000901";

function client(handler: (url: URL, init: RequestInit) => Response) {
  const calls: { url: URL; init: RequestInit }[] = [];
  const fetchImpl = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    calls.push({ url, init: init ?? {} });
    return handler(url, init ?? {});
  }) as unknown as typeof fetch;
  return { api: createCoreClient({ baseUrl: "http://core.test:8080", token: "t0k", fetchImpl }), calls };
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

describe("disputeEvalResult", () => {
  const body = { reason: "Should warn, not block.", expected_verdict: "warn" as const, surface: "web" as const, actor_label: "Dana Kim" };

  it("posts the contract's request body and returns the stored dispute", async () => {
    const dispute = loadExample<EvalDispute>("eval_dispute");
    const { api, calls } = client(() => json(dispute, 201));
    await expect(api.disputeEvalResult(RESULT, body)).resolves.toEqual(dispute);
    expect(calls[0]!.url.pathname).toBe(`/eval-results/${RESULT}/disputes`);
    expect(calls[0]!.init.method).toBe("POST");
    const sent = JSON.parse(String(calls[0]!.init.body));
    expect(sent).toEqual(body);
    expect(validateComponent("EvalDisputeRequest", sent).errors).toEqual([]);
  });

  it("surfaces the core's refusal codes", async () => {
    const { api } = client(() => json({ error: { code: "expected_equals_verdict", message: "the eval already says warn" } }, 422));
    await expect(api.disputeEvalResult(RESULT, body)).rejects.toMatchObject({ status: 422, code: "expected_equals_verdict" });
  });

  it("rejects a 2xx that is not a dispute", async () => {
    const { api } = client(() => json({ ok: true }, 201));
    await expect(api.disputeEvalResult(RESULT, body)).rejects.toBeInstanceOf(CoreError);
  });

  it("never sends a non-uuid result id to the core", async () => {
    const { api, calls } = client(() => json({}));
    await expect(api.disputeEvalResult("../runs", body)).rejects.toBeInstanceOf(InvalidIdError);
    expect(calls).toHaveLength(0);
  });
});
