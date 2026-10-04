import { firstParam } from "@/lib/params";
import { UUID } from "@/lib/uuid";
import { RunTable } from "@/components/RunTable";
import { core } from "@/lib/api/server";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;


export default async function RunsPage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const accountId = firstParam(sp.account_id);
  const status = firstParam(sp.status);
  const account = accountId && UUID.test(accountId) ? accountId : undefined;
  const runs = await core().listRuns({ limit: 50, accountId: account, status });

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
      <p className="hint">Agent runs, newest first (GET /runs). A run links its account, trigger and generation phase.</p>
      <RunTable runs={runs} />
    </section>
  );
}
