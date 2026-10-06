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
});
