import { firstParam } from "@/lib/params";
import { demoManifestFromEnv } from "@/lib/demo-manifest";
import { ManifestForm } from "@/components/replay/ManifestForm";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;


export default async function ReplayIndex({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  return (
    <section className="page">
      <h1>Episode replay</h1>
      <p>
        Step an account&apos;s chronology through the real pipeline: released history, the withheld next event, the
        account state and knowledge as of each episode, then Play next to release one event at a time.
      </p>
      <p className="hint">The demo account is preselected when one is configured; otherwise enter its manifest id.</p>
      <ManifestForm initial={firstParam(sp.manifest) ?? demoManifestFromEnv(process.env) ?? ""} />
    </section>
  );
}
