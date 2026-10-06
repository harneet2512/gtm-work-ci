// The Play strip's poll path (HAR-145): the browser reads the manifest's progress through the web app's own route (the
// core token stays server-side), and every failure keeps "backend unavailable" apart from "unknown manifest".
import { afterEach, describe, expect, it, vi } from "vitest";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { fetchReplayProgress, PROGRESS_ROUTE } from "@/lib/progress-client";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const getReplayProgress = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api/server", () => ({ core: () => ({ getReplayProgress }) }));

const { GET } = await import("@/app/api/replay-progress/[manifestId]/route");
const call = (id: string) => GET(new Request(`http://web.test${PROGRESS_ROUTE}/${id}`), { params: Promise.resolve({ manifestId: id }) });

afterEach(() => getReplayProgress.mockReset());

describe("GET the progress route", () => {
  it("returns the core's progress document, uncached", async () => {
    getReplayProgress.mockResolvedValue({ overall: "running", stages: [] });
    const res = await call(MANIFEST);
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(await res.json()).toEqual({ overall: "running", stages: [] });
    expect(getReplayProgress).toHaveBeenCalledWith(MANIFEST);
  });

  it("answers 404 for an unknown manifest", async () => {
    getReplayProgress.mockResolvedValue(null);
    const res = await call(MANIFEST);
    expect(res.status).toBe(404);
    expect(await res.json()).toEqual({ error: { code: "manifest_not_found" } });
  });

  it("answers 400 for an id that is not a uuid", async () => {
    getReplayProgress.mockRejectedValue(new InvalidIdError("../x"));
    expect((await call("..")).status).toBe(400);
  });

  it("answers 502 when the core cannot answer, with the core's code or a generic one", async () => {
    getReplayProgress.mockRejectedValue(new CoreError(0, "unreachable", "down"));
    const res = await call(MANIFEST);
    expect(res.status).toBe(502);
    expect(await res.json()).toEqual({ error: { code: "unreachable" } });
    getReplayProgress.mockRejectedValue(new Error("boom"));
    expect(await (await call(MANIFEST)).json()).toEqual({ error: { code: "backend_unavailable" } });
  });
});

describe("fetchReplayProgress", () => {
  const reply = (body: unknown, status = 200) => vi.fn(async () => new Response(JSON.stringify(body), { status })) as unknown as typeof fetch;

  it("reads a progress document from the same-origin route", async () => {
    const fetchImpl = reply({ overall: "complete", stages: [] });
    await expect(fetchReplayProgress(MANIFEST, fetchImpl)).resolves.toEqual({ ok: true, progress: { overall: "complete", stages: [] } });
    expect(vi.mocked(fetchImpl).mock.calls[0]![0]).toBe(`${PROGRESS_ROUTE}/${MANIFEST}`);
  });

  it("maps 404 to not_found and any other status, a bad body or a network error to unavailable", async () => {
    await expect(fetchReplayProgress(MANIFEST, reply({}, 404))).resolves.toEqual({ ok: false, reason: "not_found" });
    await expect(fetchReplayProgress(MANIFEST, reply({}, 502))).resolves.toEqual({ ok: false, reason: "unavailable" });
    await expect(fetchReplayProgress(MANIFEST, reply({ overall: "running" }))).resolves.toEqual({ ok: false, reason: "unavailable" });
    await expect(fetchReplayProgress(MANIFEST, reply(null))).resolves.toEqual({ ok: false, reason: "unavailable" });
    const down = vi.fn(async () => {
      throw new TypeError("fetch failed");
    }) as unknown as typeof fetch;
    await expect(fetchReplayProgress(MANIFEST, down)).resolves.toEqual({ ok: false, reason: "unavailable" });
  });
});
