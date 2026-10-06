import type { HumanDecision, HumanStrategyDecision, StrategyCandidate } from "@/lib/api/types";
import { formatUtc, shortId } from "@/lib/format";
import { literalEdits } from "@/lib/view/run-chain";
import { ArtifactView } from "./ArtifactView";

function EditLine({ kind, before, after }: { kind: string; before?: unknown; after?: unknown }) {
  const text = (v: unknown): string | null => (v === undefined || v === null ? null : typeof v === "string" ? v : JSON.stringify(v));
  const b = text(before);
  const a = text(after);
  return (
    <li>
      <code>{kind}</code>
      {b !== null || a !== null ? (
        <span className="hint">
          {" "}
          {b !== null ? `“${b}”` : "—"} → {a !== null ? `“${a}”` : "—"}
        </span>
      ) : null}
    </li>
  );
}

/**
 * What the human did (HAR-129 §8–10): the strategy decision (which candidate, edits, send decision),
 * then the HumanDecision records the run's trace carries. When nothing was chosen yet the section
 * says so; a recorded discard is shown, not hidden.
 */
export function HumanDecisionPanel({
  decision,
  decisions,
  chosen,
}: {
  decision: HumanStrategyDecision | null;
  decisions: readonly HumanDecision[];
  chosen: StrategyCandidate | null;
}) {
  if (!decision && decisions.length === 0) {
    return (
      <section aria-labelledby="human-h" className="panel">
        <h2 id="human-h">Human decision</h2>
        <p className="empty">No human decision recorded — the action is still gtm_ai's.</p>
      </section>
    );
  }
  return (
    <section aria-labelledby="human-h" className="panel">
      <h2 id="human-h">Human decision</h2>
      {decision ? (
        <>
          <dl className="kv">
            <dt>Chosen candidate</dt>
            <dd>{chosen ? `“${chosen.title}” (#${chosen.ranking})` : <code title={decision.selected_candidate_id}>{shortId(decision.selected_candidate_id)}</code>}</dd>
            <dt>By</dt>
            <dd>
              {decision.actor_label} · {decision.surface} · {formatUtc(decision.chosen_at)}
            </dd>
            <dt>Send decision</dt>
            <dd>
              {decision.send_decision}
              {decision.send_decided_at ? ` · ${formatUtc(decision.send_decided_at)}` : ""}
            </dd>
          </dl>
          {literalEdits(decision).length > 0 ? (
            <>
              <h4>Edits (literal changes)</h4>
              <ul className="eval-diffs">
                {literalEdits(decision).map((e, i) => (
                  <EditLine key={i} kind={e.kind} before={e.before} after={e.after} />
                ))}
              </ul>
            </>
          ) : (
            <p className="hint">Sent without edits.</p>
          )}
          {decision.final_artifact ? (
            <details>
              <summary>Final artifact</summary>
              <ArtifactView artifact={decision.final_artifact} to={decision.final_to} cc={decision.final_cc} />
            </details>
          ) : null}
        </>
      ) : null}
      {decisions.map((d) => (
        <div key={d.id} className="human-decision">
          <p className="summary">
            <span className={`badge dec-${d.decision}`}>{d.decision}</span> {d.actor_label} · {d.surface} · {formatUtc(d.created_at)}
          </p>
          {d.reason ? <p className="hint">{d.reason}</p> : null}
          {d.edited_artifact ? (
            <details>
              <summary>Edited artifact</summary>
              <ArtifactView artifact={d.edited_artifact} />
            </details>
          ) : null}
        </div>
      ))}
    </section>
  );
}
