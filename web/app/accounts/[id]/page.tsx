import { firstParam } from "@/lib/params";
import Link from "next/link";
import { notFound } from "next/navigation";
import { AccountWorkspace } from "@/components/AccountWorkspace";
import { TransitionBadgeView } from "@/components/TransitionBadgeView";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";
import { loadAccountPage, type AccountQuery } from "@/lib/load-account";

export const dynamic = "force-dynamic";

type Params = Promise<{ id: string }>;
type Search = Promise<Record<string, string | string[] | undefined>>;


function hrefFor(id: string, query: AccountQuery, view: "before" | "after"): string {
  const params = new URLSearchParams({ view });
  if (query.event) params.set("event", query.event);
  if (query.cutoff) params.set("cutoff", query.cutoff);
  return `/accounts/${id}?${params.toString()}`;
}

export default async function AccountPage({ params, searchParams }: { params: Params; searchParams: Search }) {
  const { id } = await params;
  const sp = await searchParams;
  const query: AccountQuery = { event: firstParam(sp.event), cutoff: firstParam(sp.cutoff), view: firstParam(sp.view) };

  let data;
  try {
    data = await loadAccountPage(core(), id, query);
  } catch (e) {
    if (e instanceof InvalidIdError || (e instanceof CoreError && e.status === 404)) notFound();
    throw e;
  }

  const current = data.view.view;
  return (
    <section className="page">
      <div className="head">
        <h1>{data.accountName}</h1>
        <TransitionBadgeView badge={data.badge} />
        <nav aria-label="Play view" className="toggle">
          <Link href={hrefFor(id, query, "before")} aria-current={current === "before" ? "page" : undefined}>
            Before Play
          </Link>
          <Link href={hrefFor(id, query, "after")} aria-current={current === "after" ? "page" : undefined}>
            After Play
          </Link>
        </nav>
      </div>
      {data.notices.length > 0 ? (
        <ul className="notices" role="status">
          {data.notices.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}
      <AccountWorkspace view={data.view} state={data.state} activities={data.view.timeline} hasEvent={data.eventId !== null} />
    </section>
  );
}
