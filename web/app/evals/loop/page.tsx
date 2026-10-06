import type { Metadata } from "next";
import { LoopLanding } from "@/components/evals/inspector/LoopLanding";
import { InspectorNav } from "@/components/evals/inspector/InspectorNav";
import { getFleet, getRegistry } from "@/lib/evals/inspector/cached";
import { buildLoop } from "@/lib/evals/inspector/loop-model";
import { firstParam } from "@/lib/params";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "How gtm_ai checks its work · gtm_ai" };

type Search = Promise<Record<string, string | string[] | undefined>>;

/** /evals/loop: the three buckets as a loop (HAR-149 section 1). The registry is the source of every word on it. */
export default async function LoopPage({ searchParams }: { searchParams: Search }) {
  const demo = firstParam((await searchParams).demo) === "1";
  const loop = buildLoop(getRegistry().defs);
  const fleet = await getFleet();
  const newest = fleet.episodes[0]?.episodeId ?? null;
  return (
    <section className="page insp-page">
      <InspectorNav active="loop" demo={demo} episodeId={newest} />
      <LoopLanding loop={loop} demo={demo} episodeHref={newest ? `/evals/episode/${newest}` : "/evals/episode"} />
    </section>
  );
}
