import Link from "next/link";
import { notFound } from "next/navigation";
import { RunChain } from "@/components/run/RunChain";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";
import { loadRunPage } from "@/lib/load-run";

export const dynamic = "force-dynamic";

type Params = Promise<{ id: string }>;

/** One agent run's decision chain (WP24): decided → why → what the human changed → what was learned. */
export default async function RunPage({ params }: { params: Params }) {
  const { id } = await params;
  let data;
  try {
    data = await loadRunPage(core(), id);
  } catch (e) {
    if (e instanceof InvalidIdError || (e instanceof CoreError && e.status === 404)) notFound();
    throw e;
  }

  return (
    <section className="page">
      <div className="head">
        <h1>Run {data.run.id.slice(0, 8)}</h1>
        <nav className="toggle" aria-label="Sections">
          <Link href={`/runs/${data.run.id}/evals`}>Evals</Link>
          <Link href="/runs">All runs</Link>
          <Link href={`/accounts/${data.run.account_id}`}>Account</Link>
        </nav>
      </div>
      <p className="hint">The decision chain of one run: what Ghost proposed, why (evals and evidence), what the human changed, and what the system learned.</p>
      <RunChain data={data} />
    </section>
  );
}
