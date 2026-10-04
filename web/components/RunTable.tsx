import Link from "next/link";
import type { AgentRun } from "@/lib/api/types";
import { formatUtc, shortId } from "@/lib/format";

/**
 * The decision runs of the demo (GET /runs), newest first: workflow, run status and the orchestrator's
 * generation phase (HAR-117 WP19) — the same status surface Slack reads.
 */
export function RunTable({ runs }: { runs: readonly AgentRun[] }) {
  if (runs.length === 0) return <p className="empty">No agent runs yet.</p>;
  return (
    <table className="runs-table">
      <thead>
        <tr>
          <th>Run</th>
          <th>Workflow</th>
          <th>Mode</th>
          <th>Status</th>
          <th>Generation</th>
          <th>Account</th>
          <th>Created</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((run) => (
          <tr key={run.id}>
            <td>
              <code title={run.id}>{shortId(run.id)}</code>
            </td>
            <td>{run.workflow}</td>
            <td>{run.run_mode}</td>
            <td>
              <span className={`badge run-${run.status}`}>{run.status}</span>
              {run.error ? <span className="hint warn" title={run.error}>error</span> : null}
            </td>
            <td>
              {run.generation ? (
                <>
                  <span className={`badge gen-${run.generation.phase}`}>{run.generation.phase}</span>
                  {run.generation.reason ? <span className="hint"> {run.generation.reason}</span> : null}
                </>
              ) : (
                <span className="empty">—</span>
              )}
            </td>
            <td>
              <Link href={`/accounts/${run.account_id}`}>
                <code>{shortId(run.account_id)}</code>
              </Link>
            </td>
            <td className="when">{formatUtc(run.created_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
