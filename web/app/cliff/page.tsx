import { redirect } from "next/navigation";
import { core } from "@/lib/api/server";
import { resolveManifest } from "@/lib/demo-manifest";
import { resolveCliffTarget } from "@/lib/load-cliff-target";
import { firstParam } from "@/lib/params";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * "Cliff messages": not a view of its own. It sends the viewer to the latest played episode with Cliff mode selected (the
 * Slack message beside its trace and evals), or says there is nothing to show yet.
 */
export default async function CliffMessages({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const { manifestId } = resolveManifest(firstParam(sp.manifest), process.env);
  const target = await resolveCliffTarget(core(), manifestId, firstParam(sp.demo) === "1");
  if (target.kind === "redirect") redirect(target.href);
  return (
    <section className="page">
      <h1>Cliff messages · M1–M3</h1>
      <p className="empty" role="status">
        {target.text}
      </p>
    </section>
  );
}
