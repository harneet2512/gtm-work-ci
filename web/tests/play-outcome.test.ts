// What the strip says when Play does not release the event: a refusal of the core is a plain sentence, a transport problem
// is neutral (nothing is claimed about the pipeline), and an unknown code is shown rather than hidden.
import { describe, expect, it } from "vitest";
import { playRefusalLine } from "@/lib/view/play-outcome";

describe("playRefusalLine", () => {
  it.each([
    ["already_released", /already completed/],
    ["play_in_progress", /already running/],
    ["held_out_event_visible", /already visible in the world/],
    ["wrong_event", /not the held-out event/],
    ["release_mismatch", /does not match the recorded event/],
    ["manifest_not_found", /demo account was not found/],
    ["graph_unavailable", /graph is not available/],
    ["replay_source_unavailable", /replay data is not available/],
  ])("words the refusal %s as a refusal", (code, text) => {
    const line = playRefusalLine(code);
    expect(line.text).toMatch(text);
    expect(line.error).toBe(true);
  });

  it("reads an unreachable backend and a timeout as backend unavailable, not as a refusal", () => {
    for (const code of ["unreachable", "timeout"]) {
      const line = playRefusalLine(code);
      expect(line.text).toMatch(/^Backend unavailable/);
      expect(line.error).toBe(false);
    }
    expect(playRefusalLine("timeout").text).toContain("press Play again to resume");
  });

  it("shows the code of a refusal it has no sentence for", () => {
    expect(playRefusalLine("odd_code")).toEqual({ text: "Play was refused (odd_code).", error: true });
  });

  it("names no endpoint, ticket or tool", () => {
    const all = ["already_released", "timeout", "unreachable", "wrong_event"].map((c) => playRefusalLine(c).text).join(" ");
    expect(all).not.toMatch(/\/replay|HAR-|ghostctl|frozen/i);
  });
});
