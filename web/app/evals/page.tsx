import type { Metadata } from "next";
import Link from "next/link";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { Explorer } from "@/components/evals/Explorer";
import { RunCompare } from "@/components/evals/RunCompare";
import { getExplorer } from "@/lib/cached-loaders";
import { loadQualityReport, qualityReportPathFromEnv } from "@/lib/evals/quality-report";
import { buildOverview, contractsDirFromEnv, loadEvalContracts } from "@/lib/evals/registry";
import { firstParam } from "@/lib/params";
import type { EvidenceClass } from "@/lib/evals/vocabulary";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "How Ghost checks its work · Ghost" };

type Search = Promise<Record<string, string | string[] | undefined>>;
type EvalsView = "catalog" | "results" | "compare";
const CLASSES: readonly EvidenceClass[] = ["product_rule", "deal_data", "methodology", "cs_ops"];
const asClass = (v: string | undefined): EvidenceClass | null => (CLASSES as readonly string[]).includes(v ?? "") ? (v as EvidenceClass) : null;
const asView = (v: string | undefined): EvalsView => (v === "results" || v === "compare" ? v : "catalog");

/**
 * The eval catalog: every eval type in the catalog and the registry, with its surface, evidence class and
 * definition, read from contracts/. Measured quality comes from the committed judge report
 * (GHOST_EVAL_QUALITY_REPORT, default bench/reports/judges-…-goldv2-…json) with its provenance; checks it does not
 * cover read "Not measured yet". `?class=` narrows the checks to one evidence class.
 */
export default async function EvalsPage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const view = asView(firstParam(sp.view));
  const active = asClass(firstParam(sp.class));

  const tabs = (
    <nav className="toggle" aria-label="Eval views">
      {(["catalog", "results", "compare"] as const).map((v) =>
        v === view ? (
          <span key={v} className="off" aria-current="page">
            {v === "catalog" ? "Checks" : v === "results" ? "Results" : "Compare"}
          </span>
        ) : (
          <Link key={v} href={`/evals?view=${v}`}>
            {v === "catalog" ? "Checks" : v === "results" ? "Results" : "Compare"}
          </Link>
        ),
      )}
    </nav>
  );

  if (view !== "catalog") {
    const data = await getExplorer();
    return (
      <section className="page eval-page">
        <header className="catalog-head explorer-head">
          <p className="eyebrow">Eval explorer</p>
          <h1>{view === "results" ? "Results" : "Compare runs"}</h1>
          {tabs}
        </header>
        {data.unavailableRuns > 0 ? (
          <p className="notices" role="status">
            Backend unavailable for {data.unavailableRuns} {data.unavailableRuns === 1 ? "run" : "runs"}: {data.unavailableRuns === 1 ? "its" : "their"} results could not be read and are not
            shown here. This is not an eval failure.
          </p>
        ) : null}
        {view === "results" ? (
          <Explorer rows={data.rows} selected={firstParam(sp.result) ?? null} />
        ) : (
          <RunCompare data={data} a={firstParam(sp.runs)?.split(",")[0] ?? null} b={firstParam(sp.runs)?.split(",")[1] ?? null} />
        )}
      </section>
    );
  }

  let overview;
  let error: string | null = null;
  try {
    const report = loadQualityReport(qualityReportPathFromEnv(process.env, process.cwd()));
    overview = buildOverview(loadEvalContracts(contractsDirFromEnv(process.env, process.cwd())), report?.byType ?? null, report?.source ?? null);
  } catch {
    error = "The eval catalog is not available right now.";
  }
  return (
    <section className="page eval-page evals-overview">
      <header className="catalog-head">
        <p className="eyebrow">Eval catalog</p>
        <h1>How Ghost checks its work</h1>
        {tabs}
        <p className="lead">
          Ghost is checked at every step of its loop: whether its understanding of the account is supported, whether its next move and what it learns from
          the human are right, and whether the machinery underneath is sound. Each eval returns a verdict and the reason behind it, never a score.
        </p>
      </header>
      {overview ? <EvalOverview overview={overview} activeClass={active} /> : <p className="notices" role="status">{error}</p>}
    </section>
  );
}
