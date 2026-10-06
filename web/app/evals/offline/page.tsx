import type { Metadata } from "next";
import { InspectorHead } from "@/components/evals/inspector/InspectorHead";
import { OfflineBody } from "@/components/evals/inspector/OfflineAndHealth";
import { getFleet, getRegistry } from "@/lib/evals/inspector/cached";
import { buildOfflineView } from "@/lib/evals/inspector/offline";
import { loadQualityReport, qualityReportPathFromEnv } from "@/lib/evals/quality-report";
import { firstParam } from "@/lib/params";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Offline checks · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;

/** /evals/offline: the capability, regression and repeated-trial checks (HAR-149 section 5). Today none of them has run. */
export default async function OfflinePage({ searchParams }: { searchParams: Search }) {
  const demo = firstParam((await searchParams).demo) === "1";
  const reg = getRegistry();
  let snapshot = false;
  try {
    snapshot = loadQualityReport(qualityReportPathFromEnv(process.env, process.cwd())) !== null;
  } catch {
    snapshot = false;
  }
  const fleet = await getFleet();
  return (
    <section className="page insp-page">
      <InspectorHead tab="offline" demo={demo} episodeId={fleet.episodes[0]?.episodeId ?? null} eyebrow="Can we trust the machinery?" title="Checks that run on fixed cases, not on customers" lead="These prove the graders and the system reliable. They are measurements of the checking itself, so they have no verdict on any one decision." />
      <OfflineBody view={buildOfflineView(reg.defs, reg.deviations, snapshot)} />
    </section>
  );
}
