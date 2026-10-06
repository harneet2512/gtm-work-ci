import { describe, expect, it } from "vitest";
import type { EpisodeSummary } from "@/lib/api/types";
import { episodeTitle } from "@/lib/view/episode";

const base = { account_name: "MedTech Advances", triggering_event: { activity_type: "email", occurred_at: "2023-11-09T09:30:00Z" } };

describe("episodeTitle", () => {
  it("names the account, the event type and the day, never an id", () => {
    expect(episodeTitle(base as unknown as EpisodeSummary)).toBe("MedTech Advances · Email · Nov 9, 2023");
  });
  it("leaves out an event that was not recorded", () => {
    expect(episodeTitle({ ...base, triggering_event: null } as unknown as EpisodeSummary)).toBe("MedTech Advances");
  });
  it("uses the graph's human label for the trigger, never a raw activity type", () => {
    const summary = { ...base, triggering_event: { activity_type: "EmailReceived", occurred_at: "2023-11-09T09:30:00Z" } } as unknown as EpisodeSummary;
    const activity = { activity_type: "EmailReceived", participants: [{ role: "from", display_name: "Fatoumata Touré", raw_identity: "f@x.com" }] } as never;
    expect(episodeTitle(summary, activity)).toBe("MedTech Advances · Email from Fatoumata · Nov 9, 2023");
    expect(episodeTitle(summary, null)).not.toMatch(/EmailReceived/);
  });
});
