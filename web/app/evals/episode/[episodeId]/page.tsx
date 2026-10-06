import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { EpisodePathView } from "@/components/evals/inspector/EpisodePathView";
import { InspectorNav } from "@/components/evals/inspector/InspectorNav";
import { getEpisodeInspector } from "@/lib/evals/inspector/cached";
import { firstParam } from "@/lib/params";
import { UUID } from "@/lib/uuid";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Live episode · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;

/** /evals/episode/[id]: the live episode path (HAR-149 section 2). Click a check to open its drawer; ?gate=D3 opens one on load. */
export default async function EpisodePathPage({ params, searchParams }: { params: Promise<{ episodeId: string }>; searchParams: Search }) {
  const { episodeId } = await params;
  const sp = await searchParams;
  const id = episodeId.trim().toLowerCase();
  if (!UUID.test(id)) notFound();
  const episode = await getEpisodeInspector(id);
  if (!episode) notFound();
  return (
    <section className="page insp-page">
      <InspectorNav active="episode" demo={firstParam(sp.demo) === "1"} episodeId={id} />
      <EpisodePathView episode={episode} demo={firstParam(sp.demo) === "1"} initialGate={firstParam(sp.gate) ?? null} />
    </section>
  );
}
