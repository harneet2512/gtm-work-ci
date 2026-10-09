// Play is the only visible trigger. When the account on screen has no episode left, the next Play releases the next
// chronological episode's Event N: the server action asks the hidden control service to hand the demo over (knowledge
// carried, graph and database switched) and then advances the next manifest through the same core endpoint. Nothing
// about that is a button the audience sees.
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AdvanceResult } from "@/lib/api/types";
import { CoreError } from "@/lib/api/core-support";

const advance = vi.hoisted(() => vi.fn());
const handoff = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api/server", () => ({ core: () => ({ advanceReplayEpisode: advance }) }));
vi.mock("@/lib/demo/server", () => ({ demoControl: () => ({ baseUrl: "http://127.0.0.1:8099", token: "t" }) }));
vi.mock("@/lib/demo/control-client", () => ({ postHandoff: handoff }));

const { playEvent } = await import("@/app/control/actions");

const FIRST = "0d3a0000-0000-4000-8000-000000000501";
const SECOND = "0d3a0000-0000-4000-8000-000000000502";
const result = (manifest: string): AdvanceResult => ({ manifest_id: manifest, account_id: "acct", episode: 1, total: 1, released: {} }) as unknown as AdvanceResult;
const complete = () => new CoreError(409, "replay_complete", "every event of the manifest's sequence is already released");

beforeEach(() => {
  advance.mockReset();
  handoff.mockReset();
});

describe("playEvent", () => {
  it("releases the next event of the account on screen and never touches the control service", async () => {
    advance.mockResolvedValueOnce(result(FIRST));
    expect(await playEvent(FIRST)).toEqual({ ok: true, result: result(FIRST) });
    expect(advance).toHaveBeenCalledExactlyOnceWith(FIRST);
    expect(handoff).not.toHaveBeenCalled();
  });

  it("continues into the next account when this one has no episode left", async () => {
    advance.mockRejectedValueOnce(complete()).mockResolvedValueOnce(result(SECOND));
    handoff.mockResolvedValueOnce({ ok: true, manifestId: SECOND, accountId: "acct-2" });
    expect(await playEvent(FIRST)).toEqual({ ok: true, result: result(SECOND) });
    expect(handoff).toHaveBeenCalledExactlyOnceWith({ baseUrl: "http://127.0.0.1:8099", token: "t" }, FIRST);
    expect(advance.mock.calls.map((c) => c[0])).toEqual([FIRST, SECOND]);
  });

  it("reports the end of the timeline as the core's own refusal when no later episode exists", async () => {
    advance.mockRejectedValueOnce(complete());
    handoff.mockResolvedValueOnce({ ok: false, code: "no_next_case" });
    expect(await playEvent(FIRST)).toEqual({ ok: false, code: "replay_complete" });
    handoff.mockResolvedValueOnce({ ok: false, code: "not_configured" });
    advance.mockRejectedValueOnce(complete());
    expect(await playEvent(FIRST)).toEqual({ ok: false, code: "replay_complete" });
  });

  it("passes any other handoff failure on as its code", async () => {
    advance.mockRejectedValue(complete());
    handoff.mockResolvedValueOnce({ ok: false, code: "handoff_failed" });
    expect(await playEvent(FIRST)).toEqual({ ok: false, code: "handoff_failed" });
    handoff.mockResolvedValueOnce({ ok: false, code: "unreachable" });
    expect(await playEvent(FIRST)).toEqual({ ok: false, code: "unreachable" });
  });

  it("does not hand over for any other refusal", async () => {
    advance.mockRejectedValueOnce(new CoreError(409, "already_released", "x"));
    expect(await playEvent(FIRST)).toEqual({ ok: false, code: "already_released" });
    advance.mockRejectedValueOnce(new Error("boom"));
    expect(await playEvent(FIRST)).toEqual({ ok: false, code: "unexpected" });
    expect(handoff).not.toHaveBeenCalled();
  });

  it("reports the failure of the next account's own Play", async () => {
    advance.mockRejectedValueOnce(complete()).mockRejectedValueOnce(new CoreError(503, "graph_unavailable", "x"));
    handoff.mockResolvedValueOnce({ ok: true, manifestId: SECOND, accountId: "acct-2" });
    expect(await playEvent(FIRST)).toEqual({ ok: false, code: "graph_unavailable" });
  });
});
