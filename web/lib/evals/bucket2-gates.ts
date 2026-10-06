// Bucket 2's gates (D1-D10) with their question and what they protect, read from the eval registry at request time,
// for surfaces that show stored results outside the evals overview (the episode page).
import { buildOverview, contractsDirFromEnv, loadEvalContracts } from "./registry";
import type { GateSourceLite } from "./bucket2-results";

export function loadBucket2Gates(env: NodeJS.ProcessEnv = process.env, cwd: string = process.cwd(), bucketId: string = "decision_action"): GateSourceLite[] {
  const overview = buildOverview(loadEvalContracts(contractsDirFromEnv(env, cwd)), null);
  const bucket = overview.buckets.find((b) => b.id === bucketId);
  return (bucket?.gates ?? []).map((g) => ({ id: g.id, name: g.name, question: g.question, improves: g.improves, built: g.built }));
}
