import Link from "next/link";
import { notFound } from "next/navigation";
import { KnowledgeDetail } from "@/components/KnowledgeDetail";
import { CoreError, InvalidIdError } from "@/lib/api/core-client";
import { core } from "@/lib/api/server";

export const dynamic = "force-dynamic";

type Params = Promise<{ id: string }>;

/** One knowledge object (GET /knowledge/{id}) — every §18 field. */
export default async function KnowledgeDetailPage({ params }: { params: Params }) {
  const { id } = await params;
  let k;
  try {
    k = await core().getKnowledge(id);
  } catch (e) {
    if (e instanceof InvalidIdError || (e instanceof CoreError && e.status === 404)) notFound();
    throw e;
  }

  return (
    <section className="page">
      <div className="head">
        <h1>{k.key ?? "Knowledge"}</h1>
        <nav className="toggle" aria-label="Sections">
          <Link href="/knowledge">All knowledge</Link>
        </nav>
      </div>
      <KnowledgeDetail k={k} />
    </section>
  );
}
