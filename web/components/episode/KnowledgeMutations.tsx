import Link from "next/link";
import type { KnowledgeMutation } from "@/lib/api/types";
import { mutationRow } from "@/lib/view/knowledge-mutations";
import { ScrollTable } from "@/components/ScrollTable";

/**
 * What the episode did to company knowledge (HAR-145): one row per piece of evidence it attached, with the operation
 * (DEMOTE included), the scope and the knowledge's version at that time. `null` means the read failed, which is not the
 * same as an episode that changed nothing.
 */
export function KnowledgeMutations({ mutations }: { mutations: KnowledgeMutation[] | null }) {
  return (
    <section className="panel knowledge-mutations" aria-labelledby="mutations-h">
      <h2 id="mutations-h">Knowledge mutations</h2>
      {mutations === null ? (
        <p className="hint">Knowledge mutations could not be read: backend unavailable.</p>
      ) : mutations.length === 0 ? (
        <p className="hint">This episode changed no company knowledge.</p>
      ) : (
        <ScrollTable label="Knowledge mutations">
          <table className="dense">
            <thead>
              <tr>
                <th>Operation</th>
                <th>Knowledge</th>
                <th>Scope</th>
                <th>Version at that time</th>
                <th>Status</th>
                <th>Human</th>
                <th>Evidence</th>
              </tr>
            </thead>
            <tbody>
              {mutations.map(mutationRow).map((r) => (
                <tr key={r.id}>
                  <td>
                    <span className={`op op-${r.operation.toLowerCase()}`}>{r.operation}</span>
                    <span className="hint"> {r.operationWord}</span>
                  </td>
                  <td>
                    <Link href={`/knowledge/${r.knowledgeId}`}>
                      <span className="mono">{r.key}</span> {r.title}
                    </Link>
                  </td>
                  <td>{r.scope}</td>
                  <td className="mono">{r.version}</td>
                  <td>{r.status}</td>
                  <td>{r.human}</td>
                  <td className="wrap">{r.evidence}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </ScrollTable>
      )}
    </section>
  );
}
