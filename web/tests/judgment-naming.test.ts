// HAR-145 UI naming map, the six display statuses and the message chips: display copy only, the backend keeps its names.
import { describe, expect, it } from "vitest";
import { AREAS, GAP_LABEL, MESSAGES, NAMING, STATUS_ORDER, displayStatus, gapLabel, messageOf, statusWord } from "@/lib/evals/naming";

describe("naming map", () => {
  it("renames exactly as HAR-145's table", () => {
    expect(NAMING).toMatchObject({
      episode: "Judgment Episode",
      decisionLearning: "Judgment Loop",
      humanSelection: "Human Judgment",
      humanDelta: "Judgment Delta",
      evalGap: "Eval Gap",
      knowledgeMutation: "Judgment Learning",
      candidateCriterion: "Candidate Judgment Rule",
      recompute: "Judgment Recompute",
      s2: "Eval Trust",
    });
  });
  it("labels the four eval-gap outcomes and leaves an unknown one as unknown", () => {
    expect(GAP_LABEL).toEqual({ COVERED: "Eval caught it", MISGRADED: "Eval judged it wrong", MISSING_CRITERION: "Missing judgment criterion", HUMAN_PREFERENCE_ONLY: "Preference only" });
    expect(gapLabel("MISGRADED")).toBe("Eval judged it wrong");
    expect(gapLabel("UNKNOWN")).toBe("Unknown");
    expect(gapLabel("nonsense")).toBe("Unknown");
    expect(gapLabel(null)).toBeNull();
  });
  it("names the nav areas with their message", () => {
    expect(AREAS.map((a) => a.label)).toEqual(["Organizational Intelligence", "Judgment Loop", "Cliff messages · M1–M3", "System Trust"]);
    expect(AREAS[0]!.message).toBe("Message 1");
    expect(AREAS[1]!.message).toBe("Messages 2–3");
  });
});

describe("the six display statuses", () => {
  it("are PASS WARN FAIL UNKNOWN NOT RUN NOT APPLICABLE", () => {
    expect(STATUS_ORDER.map(statusWord)).toEqual(["PASS", "WARN", "FAIL", "UNKNOWN", "NOT RUN", "NOT APPLICABLE"]);
  });
  it("reads a gate with no result as NOT RUN, and a conditional gate whose trigger is absent as NOT APPLICABLE", () => {
    expect(displayStatus(null, false)).toBe("not_run");
    expect(displayStatus(null, true)).toBe("not_applicable");
    expect(displayStatus("pass", false)).toBe("pass");
    expect(displayStatus("unknown", false)).toBe("unknown");
  });
  it("never lets a missing result read as pass", () => {
    expect(displayStatus(null, false)).not.toBe("pass");
    expect(displayStatus(null, true)).not.toBe("pass");
  });
});

describe("messages", () => {
  it("are the loop in order, then EcoLite Play, then system trust", () => {
    expect(MESSAGES.map((m) => m.id)).toEqual(["M1", "M2", "M3", "ecolite", "system"]);
    expect(MESSAGES.find((m) => m.id === "ecolite")!.chip).toBe("EcoLite Play");
  });
  it("resolves a registry value, and nothing for an unknown one", () => {
    expect(messageOf("M2")!.chip).toBe("M2");
    expect(messageOf(undefined)).toBeNull();
    expect(messageOf("M9")).toBeNull();
  });
});
