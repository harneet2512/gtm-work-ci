import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { EpisodeEvals } from "@/components/evals/EpisodeEvals";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";
import { formatDay } from "@/lib/format";
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
  const candidate = firstParam((await searchParams).candidate) ?? null;
  let data;
  try {
    data = await loadEvalPage(core(), id);
  } catch (e) {
    if (e instanceof InvalidIdError || (e instanceof CoreError && e.status === 404)) notFound();
    throw e;
  }

  const account = data.trace?.state_at_run?.account_name ?? null;
  const decidedOn = data.strategies?.strategy_set.generated_at ?? data.run.created_at;
  return (
    <section className="page eval-page">
      <div className="head">
        <div>
          <p className="eyebrow">Evals</p>
          <h1>{account ? `${account}: how Ghost judged this decision` : "How Ghost judged this decision"}</h1>
          <p className="hint">Decision of {formatDay(decidedOn)}. Each verdict comes with its reason and the evidence behind it; none is collapsed into a score.</p>
        </div>
        <nav className="toggle" aria-label="Related pages">
          <Link href={`/runs/${data.run.id}`}>Decision chain</Link>
          <Link href="/evals">All evals</Link>
        </nav>
      </div>
      <EpisodeEvals data={data} candidateId={candidate} dispute={disputeEval} />
    </section>
  );
}
