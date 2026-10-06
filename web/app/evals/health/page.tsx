import type { Metadata } from "next";
import { InspectorHead } from "@/components/evals/inspector/InspectorHead";
import { HealthBody } from "@/components/evals/inspector/OfflineAndHealth";
import { getHealthEpisodes } from "@/lib/evals/inspector/cached";
import { buildHealthView } from "@/lib/evals/inspector/health";
import { firstParam } from "@/lib/params";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "System health · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;

/** /evals/health: how the system runs, from stored traces and metrics (HAR-149 section 6). Metrics, never verdicts. */
export default async function HealthPage({ searchParams }: { searchParams: Search }) {
  const demo = firstParam((await searchParams).demo) === "1";
  const data = await getHealthEpisodes();
  return (
    <section className="page insp-page">
      <InspectorHead tab="health" demo={demo} episodeId={data.episodes[0]?.episodeId ?? null} eyebrow="Continuous" title="How the system is running" lead="Counts from every stored trace and run. These describe the machinery; they say nothing about whether one decision was right." />
      {data.status === "unavailable" ? (
        <p className="notices" role="alert">The traces and metrics are unavailable right now because the backend could not be reached. Nothing below is a measurement.</p>
      ) : (
        <HealthBody view={buildHealthView(data.episodes)} demo={demo} episodeIds={data.episodes.map((e) => e.episodeId)} />
      )}
    </section>
  );
}
