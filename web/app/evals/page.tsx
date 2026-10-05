import type { Metadata } from "next";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { buildOverview, contractsDirFromEnv, loadEvalContracts } from "@/lib/evals/registry";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "How Ghost checks its work · Ghost" };

/**
 * The eval overview: every eval type in the catalog and the registry, with its surface, evidence class and
 * definition, read from contracts/. Measured quality is null until the core serves the eval-of-evals report
 * (WP23, HAR-121); no number is shown that was not measured.
 */
export default function EvalsPage() {
  let overview;
  let error: string | null = null;
  try {
    overview = buildOverview(loadEvalContracts(contractsDirFromEnv(process.env, process.cwd())), null);
  } catch {
    error = "The eval registry could not be read. Set GHOST_CONTRACTS_DIR to the repository's contracts directory.";
  }
  return (
    <section className="page eval-page evals-overview">
      <div className="head">
        <div>
          <p className="eyebrow">Evals</p>
          <h1>How Ghost checks its work</h1>
          <p className="hint">Before a seller sees an action, Ghost runs the evals that apply to it. Each returns a verdict and the reason behind it, never a score.</p>
        </div>
      </div>
      {overview ? <EvalOverview overview={overview} /> : <p className="notices" role="status">{error}</p>}
    </section>
  );
}
