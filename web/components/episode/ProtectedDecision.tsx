import { exampleFor, type GateCard } from "@/lib/evals/gate-cards";
import { episodeProtection } from "@/lib/evals/gate-scopes";
import type { GateRow } from "@/lib/evals/gate-table";
import { VerdictPill } from "@/components/evals/gates/VerdictPill";
import { ModeBadge } from "@/components/evals/gates/ModeBadge";

/**
 * "What protected this decision?": only this episode's live and conditional gates, in the causal order INGEST, DECIDE, HUMAN,
 * SEND. A conditional gate carries "?" and, when it did not run, the reason. Calibration is a separate row, never folded
 * into a PASS. Offline and continuous gates live in System Proof, not here.
 */
export function ProtectedDecision({ cards, rows, demo = false }: { cards: readonly GateCard[]; rows: readonly GateRow[]; demo?: boolean }) {
  const phases = episodeProtection(cards);
  return (
    <section id="protected-decision" className="panel" aria-labelledby="protected-h" data-view="episode-protection">
      <h3 id="protected-h">What protected this decision?</h3>
      <p className="hint">Only the gates that apply to this episode, in the order they ran. A "?" marks a gate that runs only when its trigger exists.</p>
      {phases.map((p) => (
        <div key={p.phase} className="protection-phase" data-phase={p.phase}>
          <h4>{p.phase}</h4>
          <ul className="protection-list">
            {p.gates.map((g) => {
              const result = exampleFor(g.id, rows);
              const measured = rows.some((r) => r.gate === g.id && r.measured);
              return (
                <li key={g.id} data-gate={g.id} data-conditional={g.conditional}>
                  <span className="mono gate-id">{`${g.id}${g.conditional ? "?" : ""}`}</span>
                  <strong>{g.card.name}</strong>
                  <ModeBadge card={g.card} demo={demo} />
                  {measured && result ? <VerdictPill verdict={result.verdict} /> : <VerdictPill verdict={null} notTriggered={g.conditional} />}
                  {measured && result ? (
                    <span className="hint">{result.observed}</span>
                  ) : g.conditional ? (
                    <span className="hint">{`Did not run: ${g.skipReason ?? "its trigger did not occur"}`}</span>
                  ) : (
                    <span className="hint">Not run yet</span>
                  )}
                  {measured && g.card.notCalibrated ? <span className="uncal-tag">Calibration: not yet calibrated</span> : null}
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </section>
  );
}
