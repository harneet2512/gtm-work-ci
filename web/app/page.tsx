import Link from "next/link";
import { core } from "@/lib/api/server";
import { loadAccounts } from "@/lib/load-lists";
import { firstParam } from "@/lib/params";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

/** Home: the accounts Ghost watches, by name, a page at a time. An unreachable backend is said, never shown as "no accounts". */
export default async function AccountsPage({ searchParams }: { searchParams: Search }) {
  const data = await loadAccounts(core(), firstParam((await searchParams).cursor));
  return (
    <section className="page">
      <h1>Accounts</h1>
      {data.unavailable ? (
        <p className="notices" role="status">
          Backend unavailable: the accounts could not be read. This does not mean there are none.
        </p>
      ) : null}
      {data.badCursor ? (
        <p className="notices" role="status">
          That page marker was not valid, so the first accounts are shown.
        </p>
      ) : null}
      {!data.unavailable && data.items.length === 0 ? (
        <p className="empty">There are no accounts yet.</p>
      ) : (
        <ul className="account-list">
          {data.items.map((a) => (
            <li key={a.id}>
              <Link href={`/accounts/${a.id}`}>{a.name}</Link>
              <span className="hint">{[a.stage, a.health, a.motion].filter(Boolean).join(" · ")}</span>
            </li>
          ))}
        </ul>
      )}
      {data.nextCursor ? (
        <p>
          <Link href={`/?cursor=${encodeURIComponent(data.nextCursor)}`}>More accounts</Link>
        </p>
      ) : null}
    </section>
  );
}
