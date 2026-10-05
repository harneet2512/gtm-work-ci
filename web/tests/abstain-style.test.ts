// abstain is "Ghost is unsure" (UNKNOWN), not a warning: warn means a check found a problem; unsure means it could not
// decide. A CSS rule whose selector names abstain must therefore never paint with the warn palette.
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { buildControlView } from "@/lib/view/control";
import type { AgentRun, EpisodeReplayView, RunStrategies } from "@/lib/api/types";
import { loadFixture } from "./contract-validator";

const dir = path.resolve(__dirname, "../app/styles");

describe("abstain styling", () => {
  const files = readdirSync(dir).filter((f) => f.endsWith(".css"));
  it.each(files)("%s never paints abstain with the warn palette", (file) => {
    const css = readFileSync(path.join(dir, file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    const rules = css.split("}").map((r) => r.split("{") as [string, string?]);
    for (const [selector, body] of rules) {
      if (body && /abstain/.test(selector)) expect(body, `${file}: ${selector.trim()}`).not.toMatch(/--warn/);
    }
  });
});

const view = loadFixture<EpisodeReplayView>("replay.episodes.json");
const strategies = loadFixture<RunStrategies>("medtech.run-strategies.json");
const run = { ...loadFixture<AgentRun>("medtech.agent-run.json"), account_id: view.account_id } as AgentRun;

const onlyVerdicts = (verdicts: string[]): RunStrategies =>
  ({ ...strategies, eval_bundles: [{ ...strategies.eval_bundles[0]!, items: verdicts.map((verdict) => ({ ...strategies.eval_bundles[0]!.items[0]!, verdict })) }] }) as RunStrategies;

const tone = (verdicts: string[]) =>
  buildControlView(view, null, run, onlyVerdicts(verdicts), null, []).bands.find((b) => b.id === "decision_learning")!.tone;

describe("decision band tone", () => {
  it("is unsure - not warn - when the only non-pass verdicts are abstains", () => {
    expect(tone(["pass", "abstain"])).toBe("unsure");
  });
  it("still ranks fail over warn over unsure over ok", () => {
    expect(tone(["abstain", "warn"])).toBe("warn");
    expect(tone(["abstain", "warn", "fail"])).toBe("fail");
    expect(tone(["pass", "pass"])).toBe("ok");
  });
  it("is none when nothing was checked (only not_relevant verdicts)", () => {
    expect(tone(["not_relevant"])).toBe("none");
  });
});
