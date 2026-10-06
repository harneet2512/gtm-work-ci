import type { GateCard } from "@/lib/evals/gate-cards";
import { systemProof } from "@/lib/evals/gate-scopes";
import { MODES } from "@/lib/evals/gate-cards";

/**
 * System Proof: "How do we know the eval system is trustworthy?". Eval Trust (S2), S3, S4 and S5, then the offline gold and
 * regression coverage of each B and D gate. It never shows a live episode verdict: those live in the episode's own view.
 * A count or a last-run date appears only where the backend exposes one; none does today, so each reads "not measured".
 */
export function SystemProof({ cards }: { cards: readonly GateCard[] }) {
  const proof = systemProof(cards);
  return (
    <div className="proof-split" data-view="system-proof">
      <section aria-labelledby="proof-trust">
        <h3 id="proof-trust">Eval Trust and system gates</h3>
        <p className="hint">Calibration: {proof.calibration}. The reference answers come from a stronger model, not humans, and the calibration run is pending.</p>
        <ul className="protection-list">
          {proof.trust.map((c) => (
            <li key={c.id} id={`proof-${c.id}`} data-gate={c.id}>
              <span className="mono gate-id">{c.id}</span>
              <strong>{c.name}</strong>
              <span className="mode-badge">{MODES[c.mode].badge}</span>
              <span className="hint">{c.statusNote ?? c.question}</span>
              <span className="impact-line"><strong>What failure changes:</strong> {c.impact ?? "not stated"}</span>
            </li>
          ))}
        </ul>
      </section>
      <section aria-labelledby="proof-coverage">
        <h3 id="proof-coverage">Offline gold and regression coverage per gate</h3>
        <table className="proof-table">
          <thead>
            <tr><th scope="col">Gate</th><th scope="col">Name</th><th scope="col">Cases</th><th scope="col">Last run</th></tr>
          </thead>
          <tbody>
            {proof.coverage.map((r) => (
              <tr key={r.id} data-gate={r.id}>
                <td className="mono">{r.id}</td>
                <td>{r.name}</td>
                <td className="hint">{r.cases}</td>
                <td className="hint">{r.lastRun}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}
