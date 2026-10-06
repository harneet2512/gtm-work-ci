// @vitest-environment jsdom
// Judge quality is a recorded snapshot of an EARLIER judge against reference answers by a stronger model (not human). The
// page must say so in plain words (no file names) and must not claim it was re-measured on the judge that runs today.
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { JudgeQuality } from "@/components/evals/JudgeQuality";
import { buildOverview, loadEvalContracts, contractsDirFromEnv } from "@/lib/evals/registry";
import { JUDGE_QUALITY_CAVEAT } from "@/lib/evals/quality-report";

afterEach(cleanup);
const contracts = loadEvalContracts(contractsDirFromEnv({}, process.cwd()));
const source = { recordedAt: "2026-10-03T00:00:00Z", judged: 40, agreement: 0.9, falsePass: 0.05, falseBlock: 0.02 };

describe("judge-quality caveat", () => {
  it("names the three limits in plain words", () => {
    expect(JUDGE_QUALITY_CAVEAT).toMatch(/earlier (AI )?judge/i);
    expect(JUDGE_QUALITY_CAVEAT).toMatch(/reference answers by a stronger model, not human/i);
    expect(JUDGE_QUALITY_CAVEAT).toMatch(/not (been )?re-?measured/i);
    expect(JUDGE_QUALITY_CAVEAT).not.toMatch(/\.(json|md|ts)\b|deepseek|qwen|gold/i);
  });

  it("shows the caveat beside a recorded snapshot", () => {
    render(<JudgeQuality overview={buildOverview(contracts, new Map([["grounding", { agreement: 0.9, falsePass: 0.05, falseBlock: 0.02, cases: 40, report: "Recorded Oct 3, 2026" }]]), source)} />);
    expect(screen.getByText(JUDGE_QUALITY_CAVEAT)).toBeTruthy();
  });

  it("shows no caveat when nothing was recorded (there is no snapshot to qualify)", () => {
    render(<JudgeQuality overview={buildOverview(contracts, null)} />);
    expect(screen.queryByText(JUDGE_QUALITY_CAVEAT)).toBeNull();
  });
});
