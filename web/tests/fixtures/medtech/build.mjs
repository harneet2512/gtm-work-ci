// Writes the MedTech Advances fixtures to tests/fixtures/core/medtech.*.json. Run after editing the case:
//   node tests/fixtures/medtech/build.mjs
// tests/medtech-fixtures.test.ts fails when a committed file differs from what this produces.
import { writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { agentRun, judgmentInference, runTrace, strategyDecision } from "./episode.mjs";
import { runStrategies } from "./strategies.mjs";
import { accountSummary, graphAfter, graphBefore, graphDiff, stateAfter, stateBefore, timeline } from "./world.mjs";

export const MEDTECH_FILES = {
  "medtech.account.json": accountSummary,
  "medtech.graph.json": graphAfter,
  "medtech.graph-before.json": graphBefore,
  "medtech.graph-diff.json": graphDiff,
  "medtech.timeline.json": timeline,
  "medtech.state.json": stateAfter,
  "medtech.state-before.json": stateBefore,
  "medtech.agent-run.json": agentRun,
  "medtech.run-trace.json": runTrace,
  "medtech.run-strategies.json": runStrategies,
  "medtech.strategy-decision.json": strategyDecision,
  "medtech.judgment-inference.json": judgmentInference,
};

export const serialize = (doc) => `${JSON.stringify(doc, null, 2)}\n`;

const here = path.dirname(fileURLToPath(import.meta.url));
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  for (const [file, make] of Object.entries(MEDTECH_FILES)) {
    writeFileSync(path.resolve(here, "../core", file), serialize(make()));
  }
  console.log(`wrote ${Object.keys(MEDTECH_FILES).length} MedTech fixtures`);
}
