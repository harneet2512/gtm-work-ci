import { firstParam } from "@/lib/params";
import { ManifestForm } from "@/components/replay/ManifestForm";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;


export default async function ReplayIndex({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  return (
    <section className="page">
      <h1>Episode replay</h1>
      <p>
        Step a frozen demo manifest through the real pipeline (HAR-129 §B/§C): released history, the withheld next
        event, the account state and knowledge as of each episode bound, then Play next to release one event at a
        time.
      </p>
      <p className="hint">
        The core does not expose a manifest list — enter the manifest id of the demo account.
      </p>
      <ManifestForm initial={firstParam(sp.manifest) ?? ""} />
    </section>
  );
}
