import Link from "next/link";
import type { Knowledge } from "@/lib/api/types";
import { formatUtc } from "@/lib/format";
import { countsLine, statusBadge } from "@/lib/view/knowledge";

/** The knowledge list (GET /knowledge): human key, lifecycle status, title and evidence counts. */
export function KnowledgeTable({ items }: { items: readonly Knowledge[] }) {
  if (items.length === 0) return <p className="empty">No knowledge objects yet — the learning loop writes them as decisions accumulate.</p>;
  return (
    <table className="runs-table">
      <thead>
        <tr>
          <th>Key</th>
          <th>Status</th>
          <th>Knowledge</th>
          <th>Evidence</th>
          <th>Last validated</th>
        </tr>
      </thead>
      <tbody>
        {items.map((k) => (
          <tr key={k.id}>
            <td>
              <Link href={`/knowledge/${k.id}`}>
                <code>{k.key ?? k.id.slice(0, 8)}</code>
              </Link>
            </td>
            <td>
              <span className={statusBadge(k.status)}>{k.status}</span>
            </td>
            <td className="summary">{k.title}</td>
            <td className="hint">{countsLine(k)}</td>
            <td className="when">{k.last_validated_at ? formatUtc(k.last_validated_at) : "—"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
