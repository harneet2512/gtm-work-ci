import type { Metadata } from "next";
import { EpisodePicker } from "@/components/evals/inspector/EpisodePicker";
import { InspectorHead } from "@/components/evals/inspector/InspectorHead";
import { getFleet } from "@/lib/evals/inspector/cached";
import { firstParam } from "@/lib/params";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Pick an episode · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;

/** /evals/episode: pick an episode, then follow it through the checks that applied (HAR-149 section 2). */
export default async function EpisodePickerPage({ searchParams }: { searchParams: Search }) {
  const demo = firstParam((await searchParams).demo) === "1";
  const fleet = await getFleet();
  return (
    <section className="page insp-page">
      <InspectorHead tab="episode" demo={demo} episodeId={fleet.episodes[0]?.episodeId ?? null} eyebrow="Live episode" title="Which episode do you want to follow?" lead="Each one is a real event that gtm_ai read, and the decision it led to." />
      {fleet.status === "unavailable" ? <p className="notices" role="alert">The episodes are unavailable right now because the backend could not be reached. This says nothing about any check.</p> : null}
      <EpisodePicker demo={demo} episodes={fleet.episodes.map((e) => ({ episodeId: e.episodeId, label: e.label, summary: e.summary, resultCount: e.results.length }))} />
    </section>
  );
}
