import type { Metadata } from "next";
import Link from "next/link";
import { EvalOverview } from "@/components/evals/EvalOverview";
import { EvalRuns } from "@/components/evals/EvalRuns";
import { RunCompare } from "@/components/evals/RunCompare";
import { core } from "@/lib/api/server";
import { getEvalFamilies, getEvalRuns } from "@/lib/cached-loaders";
import { loadQualityReport, qualityReportPathFromEnv } from "@/lib/evals/quality-report";
import { buildOverview, contractsDirFromEnv, loadEvalContracts } from "@/lib/evals/registry";
import { loadComparison } from "@/lib/load-eval-runs";
import { GateCards } from "@/components/evals/gates/GateCards";
import { modeOf, buildGateCards, definitionsById, gateFromParam, type GateDefinitionSource } from "@/lib/evals/gate-cards";
import { GatesView } from "@/components/evals/gates/GatesView";
import { loadGateTable } from "@/lib/evals/load-gate-table";
import { loadLatestGateResults } from "@/lib/evals/load-gate-results";
import { firstParam } from "@/lib/params";
import { evalsTabs, resolveEvalsView } from "@/lib/view/eval-runs";
import { UUID } from "@/lib/uuid";
import type { EvidenceClass } from "@/lib/evals/vocabulary";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "How gtm_ai checks its work · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;
const CLASSES: readonly EvidenceClass[] = ["product_rule", "deal_data", "methodology", "cs_ops"];
const asClass = (v: string | undefined): EvidenceClass | null => ((CLASSES as readonly string[]).includes(v ?? "") ? (v as EvidenceClass) : null);

/**
 * /evals: how gtm_ai checks its work. Checks is the catalog of every eval, by job; Results is the explorer over the recorded
 * eval runs (areas, families and eval types with real counts, deltas against the previous episode); the run comparison is an
 * operator-only view (one trigger, before and after) that Demo mode never lists. `?class=` narrows the checks to one evidence class.
 */
export default async function EvalsPage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const demo = firstParam(sp.demo) === "1";
  const view = resolveEvalsView(firstParam(sp.view), demo);
  const active = asClass(firstParam(sp.class));

  const tabs = (
    <nav className="toggle" aria-label="Eval views">
      {evalsTabs(demo).map((t) =>
        t.id === view ? (
          <span key={t.id} className="off" aria-current="page">
            {t.label}
          </span>
        ) : (
          <Link key={t.id} href={`/evals?view=${t.id}${demo ? "&demo=1" : ""}`}>
            {t.label}
          </Link>
        ),
      )}
    </nav>
  );

  if (view === "cards") {
    const registry = loadEvalContracts(contractsDirFromEnv(process.env, process.cwd())).registry;
    const gates = registry.gates as unknown as GateDefinitionSource[];
    const data = await loadGateTable(core(), gates.map((g) => ({ id: g.id, question: g.question, improves: g.improves, mode: modeOf(g.mode), grader: g.grader, notTriggered: g.not_triggered })), firstParam(sp.episode));
    return (
      <section className="page eval-page gates-page">
        <header className="catalog-head explorer-head">
          <p className="eyebrow">Eval gates</p>
          <h1>What each gate checks</h1>
          {tabs}
        </header>
        {data.status === "unavailable" ? <p className="notices" role="alert">The latest results are unavailable right now because the backend could not be reached. This says nothing about any gate.</p> : null}
        <GateCards buckets={buildGateCards(gates, registry.buckets, data.rows)} demo={demo} />
      </section>
    );
  }

  if (view === "gates") {
    const registry = loadEvalContracts(contractsDirFromEnv(process.env, process.cwd())).registry;
    const defs = registry.gates as unknown as GateDefinitionSource[];
    const catalog = defs.map((g) => ({ id: g.id, question: g.question, improves: g.improves, mode: modeOf(g.mode), grader: g.grader, notTriggered: g.not_triggered }));
    const data = await loadGateTable(core(), catalog, firstParam(sp.episode));
    return (
      <section className="page eval-page gates-page">
        <header className="catalog-head explorer-head">
          <p className="eyebrow">Eval results</p>
          <h1>Gate results</h1>
          {tabs}
        </header>
        <GatesView data={data} gates={catalog.filter((c) => c.mode === "live_required" || c.mode === "live_conditional").map((c) => c.id)} modes={Object.fromEntries(defs.map((g) => [g.id, modeOf(g.mode)]))} defs={definitionsById(defs)} initialGate={gateFromParam(firstParam(sp.gate), catalog.map((c) => c.id))} />
      </section>
    );
  }

  if (view === "results") {
    const rawRun = firstParam(sp.run)?.trim().toLowerCase();
    const selected = rawRun && UUID.test(rawRun) ? rawRun : null;
    const [data, families] = await Promise.all([getEvalRuns(firstParam(sp.cursor)), selected ? getEvalFamilies(selected) : Promise.resolve(null)]);
    return (
      <section className="page eval-page">
        <header className="catalog-head explorer-head">
          <p className="eyebrow">Eval explorer</p>
          <h1>Results</h1>
          {tabs}
        </header>
        <EvalRuns data={data} selectedId={selected} families={families} />
      </section>
    );
  }

  if (view === "compare") {
    const a = firstParam(sp.a)?.trim().toLowerCase();
    const b = firstParam(sp.b)?.trim().toLowerCase();
    const [runs, comparison] = await Promise.all([getEvalRuns(undefined), loadComparison(core(), a, b)]);
    return (
      <section className="page eval-page">
        <header className="catalog-head explorer-head">
          <p className="eyebrow">Eval explorer</p>
          <h1>Run comparison</h1>
          {tabs}
        </header>
        <RunCompare data={comparison} runs={runs.runs} a={a ?? null} b={b ?? null} />
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
  const { results: gateResults, episodeId: gateEpisodeId } = await loadLatestGateResults(core(), firstParam(sp.episode));
  return (
    <section className="page eval-page evals-overview">
      <header className="catalog-head">
        <p className="eyebrow">Eval catalog</p>
        <h1>How gtm_ai checks its work</h1>
        {tabs}
        <p className="lead">
          gtm_ai is checked at every step of its loop: whether its understanding of the account is supported, whether its next move and what it learns from
          the human are right, and whether the machinery underneath is sound. Each eval returns a verdict and the reason behind it, never a score.
        </p>
      </header>
      {overview ? <EvalOverview overview={overview} activeClass={active} demo={demo} gateResults={gateResults} gateEpisodeId={gateEpisodeId} /> : <p className="notices" role="status">{error}</p>}
    </section>
  );
}
