import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { EpisodeEvals } from "@/components/evals/EpisodeEvals";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";
import { loadJobFamilies } from "@/lib/evals/job-families";
import { loadEvalPage } from "@/lib/load-eval-page";
import { firstParam } from "@/lib/params";
import { disputeEval } from "./actions";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Evals · Ghost" };

type Params = Promise<{ id: string }>;
type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * The episode eval page (eval design spec 4b-3): how Ghost judged the three options of one decision, and the
 * chosen action's evals one by one with their evidence. `?candidate=<id>` opens another option's evals.
 */
export default async function RunEvalsPage({ params, searchParams }: { params: Params; searchParams: Search }) {
  const { id } = await params;
  const sp = await searchParams;
  const candidate = firstParam(sp.candidate) ?? null;
  const demo = firstParam(sp.demo) === "1";
  let data;
  try {
    data = await loadEvalPage(core(), id);
  } catch (e) {
    if (e instanceof InvalidIdError || (e instanceof CoreError && e.status === 404)) notFound();
    throw e;
  }
  return (
    <section className="page eval-page">
      <EpisodeEvals data={data} candidateId={candidate} dispute={disputeEval} intelligenceFamilies={loadJobFamilies("intelligence", process.env, process.cwd())} demo={demo} />
    </section>
  );
}
