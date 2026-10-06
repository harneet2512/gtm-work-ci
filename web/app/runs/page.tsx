import Link from "next/link";
import { firstParam } from "@/lib/params";
import { UUID } from "@/lib/uuid";
import { RunTable } from "@/components/RunTable";
import { core } from "@/lib/api/server";
import { loadRuns } from "@/lib/load-lists";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

/** Decision runs, newest first, a page at a time, filterable by account and status. */
export default async function RunsPage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const accountId = firstParam(sp.account_id);
  const status = firstParam(sp.status)?.trim() || undefined;
  const account = accountId && UUID.test(accountId) ? accountId : undefined;
  const data = await loadRuns(core(), { accountId: account, status, cursor: firstParam(sp.cursor) });

  const next = new URLSearchParams();
  if (account) next.set("account_id", account);
  if (status) next.set("status", status);
  if (data.nextCursor) next.set("cursor", data.nextCursor);

  return (
    <section className="page">
      <div className="head">
        <h1>Decision runs</h1>
        <form method="get" className="filters">
          <input name="account_id" placeholder="account id (uuid)" defaultValue={account ?? accountId ?? ""} spellCheck={false} />
          <input name="status" placeholder="status" defaultValue={status ?? ""} spellCheck={false} />
          <button type="submit">Filter</button>
        </form>
      </div>
      {accountId && !account ? <p className="notices">The account id in the URL is not a uuid, so it was ignored.</p> : null}
      {data.badFilter ? (
        <p className="notices" role="status">
          That filter was not accepted, so no runs are shown for it.
        </p>
      ) : null}
      {data.unavailable ? (
        <p className="notices" role="status">
          Backend unavailable: the runs could not be read. This does not mean there are none.
        </p>
      ) : null}
      {data.badCursor ? (
        <p className="notices" role="status">
          That page marker was not valid, so the newest runs are shown.
        </p>
      ) : null}
      <p className="hint">Agent runs, newest first. A run links its account, trigger and generation phase.</p>
      {data.unavailable || data.badFilter ? null : <RunTable runs={data.items} />}
      {data.nextCursor ? (
        <p>
          <Link href={`/runs?${next.toString()}`}>Older runs</Link>
        </p>
      ) : null}
    </section>
  );
}
