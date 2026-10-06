import Link from "next/link";
import { firstParam } from "@/lib/params";
import { getEvalRuns } from "@/lib/cached-loaders";
import { evalRunRow } from "@/lib/view/eval-runs";
import { formatUtc } from "@/lib/format";
import { UUID } from "@/lib/uuid";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

const hint = (text: string) => (
  <div className="inspector-pane">
    <h2>Inspector</h2>
    <p className="hint">{text}</p>
  </div>
);

/**
 * The eval-run inspector (HAR-145): `?run=<id>` on /evals?view=results shows the selected run at a glance (account, time,
 * how many results, each area's counts) with the doors into its evals and its episode. The detail by area, family and eval
 * type is the page itself.
 */
export default async function EvalsInspector({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  if (firstParam(sp.view) !== "results") return null; // the gate results table carries its own inspector pane
  const runId = firstParam(sp.run)?.trim().toLowerCase() ?? null;
  if (!runId || !UUID.test(runId) || firstParam(sp.view) !== "results") return hint("Select an eval run to see its areas, families and eval types.");

  const { runs } = await getEvalRuns(firstParam(sp.cursor));
  const run = runs.find((r) => r.id === runId);
  if (!run) return hint(`Run ${runId.slice(0, 8)} is not on this page of runs.`);

  const row = evalRunRow(run);
  return (
    <div className="inspector-pane">
      <h2>{row.account}</h2>
      <p className="hint">
        {formatUtc(row.evaluatedAt)} · {row.total}
      </p>
      <dl className="kv">
        {row.areas.map((a) => (
          <div key={a.id}>
            <dt>{a.label}</dt>
            <dd>{a.text}</dd>
          </div>
        ))}
      </dl>
      <p>
        <Link href={`/runs/${row.id}/evals`}>This run&apos;s evals with their reasons →</Link>
      </p>
      {row.episodeId ? (
        <p>
          <Link href={`/episodes/${row.episodeId}`}>Its episode →</Link>
        </p>
      ) : null}
    </div>
  );
}
