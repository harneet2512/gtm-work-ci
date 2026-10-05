import { firstParam } from "@/lib/params";
import { KnowledgeTable } from "@/components/KnowledgeTable";
import { core } from "@/lib/api/server";
import { KNOWLEDGE_STATUSES, parseKnowledgeStatus } from "@/lib/view/knowledge";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

/** The learned-knowledge list (GET /knowledge, HAR-97 §18), filterable by lifecycle status. */
export default async function KnowledgePage({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const raw = firstParam(sp.status);
  const status = parseKnowledgeStatus(raw);
  const items = await core().listKnowledge({ status: status ?? undefined, limit: 200 });

  return (
    <section className="page">
      <div className="head">
        <h1>Knowledge</h1>
        <form method="get" className="filters">
          <select name="status" defaultValue={status ?? ""} aria-label="Lifecycle status">
            <option value="">all statuses</option>
            {KNOWLEDGE_STATUSES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          <button type="submit">Filter</button>
        </form>
      </div>
      {raw && !status ? <p className="notices">The status “{raw}” is not a lifecycle status, so the filter was ignored.</p> : null}
      <p className="hint">What the organization learned from decided actions: each object's guidance, evidence and lifecycle is on its detail page.</p>
      <KnowledgeTable items={items} />
    </section>
  );
}
