// One job's eval families from the registry (contracts/evals/eval_registry.json `job`), for pages that show a job's
// build status beside the objects it judges. Server-only (reads the contracts directory); none when unreadable.
import { buildOverview, contractsDirFromEnv, loadEvalContracts, type EvalJob, type Family } from "./registry";

export function loadJobFamilies(job: EvalJob, env: Readonly<Record<string, string | undefined>>, cwd: string): Family[] {
  try {
    return buildOverview(loadEvalContracts(contractsDirFromEnv(env, cwd)), null).families.filter((f) => f.job === job);
  } catch {
    return [];
  }
}
