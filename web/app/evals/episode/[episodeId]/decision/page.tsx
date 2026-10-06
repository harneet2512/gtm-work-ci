import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DecisionView } from "@/components/evals/inspector/DecisionView";
import { InspectorHead } from "@/components/evals/inspector/InspectorHead";
import { getEpisodeInspector } from "@/lib/evals/inspector/cached";
import { firstParam } from "@/lib/params";
import { UUID } from "@/lib/uuid";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "The three options · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;

/** /evals/episode/[id]/decision: the three options side by side, judged, ranked, and what the human did with them (HAR-149 section 4). */
export default async function DecisionPage({ params, searchParams }: { params: Promise<{ episodeId: string }>; searchParams: Search }) {
  const { episodeId } = await params;
  const demo = firstParam((await searchParams).demo) === "1";
  const id = episodeId.trim().toLowerCase();
  if (!UUID.test(id)) notFound();
  const episode = await getEpisodeInspector(id);
  if (!episode) notFound();
  return (
    <section className="page insp-page">
      <InspectorHead tab="decision" demo={demo} episodeId={id} eyebrow={episode.label} title="Three options, one recommendation" lead="Each option is judged on its own criteria. The one gtm_ai recommends is marked, and so is the one the human chose." />
      {episode.notices.map((t) => (
        <p key={t} className="notices" role="status">{t}</p>
      ))}
      <DecisionView screen={episode.screen} />
    </section>
  );
}
