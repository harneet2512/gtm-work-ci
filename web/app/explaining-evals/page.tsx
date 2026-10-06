import type { Metadata } from "next";
import { ExplainerView } from "@/components/evals/explainer/ExplainerView";
import { buildExplainerPage, loadExplainer } from "@/lib/evals/explainer";
import { contractsDirFromEnv, loadEvalContracts } from "@/lib/evals/registry";
import { firstParam } from "@/lib/params";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Explaining evals · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * /explaining-evals: what each eval checks, why gtm_ai needs it, when it starts and what happens with its answer, in the
 * order of the product loop. Read only: the copy is contracts/evals/explainer.v1.json and the registry says which evals
 * exist yet. No core is needed.
 */
export default async function ExplainingEvalsPage({ searchParams }: { searchParams: Search }) {
  const demo = firstParam((await searchParams).demo) === "1";
  const dir = contractsDirFromEnv(process.env, process.cwd());
  const gates = new Set(loadEvalContracts(dir).registry.gates.map((g) => g.id));
  return <ExplainerView page={buildExplainerPage(loadExplainer(dir), gates)} demo={demo} />;
}
