import Link from "next/link";
import { withDemo } from "@/lib/view/demo-link";
import type { Family } from "@/lib/evals/registry";
import type { Understanding, UnderstoodChange } from "@/lib/evals/understanding";

const STANDING: Readonly<Record<string, string>> = {
  human_approved: "Confirmed by a person",
  crm_explicit: "CRM record",
  first_party_record: "Recorded by the customer",
  first_party_ai: "Inferred from the customer's words",
  third_party: "Third-party data",
};

function Change({ c }: { c: UnderstoodChange }) {
  const e = c.evidence.find((x) => x.quote) ?? c.evidence[0];
  return (
    <li className="understood">
      <div className="understood-head">
        <span className="understood-label">{c.label}</span>
        {c.standing ? (
          <span className="standing-chip">
            {STANDING[c.standing] ?? c.standing}
            {c.confidence !== null ? ` · ${Math.round(c.confidence * 100)}%` : ""}
          </span>
        ) : null}
      </div>
      {c.added.length > 0 || c.removed.length > 0 ? (
        <ul className="understood-delta">
          {c.added.map((v) => (
            <li key={`+${v}`} className="is-added">
              <span className="sr-only">Added: </span>
              {v}
            </li>
          ))}
          {c.removed.map((v) => (
            <li key={`-${v}`} className="is-removed">
              <span className="sr-only">Removed: </span>
              {v}
            </li>
          ))}
        </ul>
      ) : (
        <p className="understood-value">
          {c.before && c.before !== c.after ? (
            <>
              <span className="was">{c.before}</span>
              <span className="arrow" aria-label="became">
                →
              </span>
            </>
          ) : null}
          <span className="now">{c.after ?? "unknown"}</span>
        </p>
      )}
      {e ? (
        <p className="understood-evidence">
          {e.quote ? <q>{e.quote}</q> : null} <span className="hint">{[e.who, e.when, e.source].filter(Boolean).join(" · ")}</span>
        </p>
      ) : (
        <p className="hint">No evidence recorded for this change.</p>
      )}
    </li>
  );
}

/**
 * Job 1 of the loop (HAR-129 demo-loop clarification): what gtm_ai now understands because Event N arrived, the
 * object the intelligence-building evals judge, shown before any strategy. Every change carries the words it rests
 * on. Per-event verdicts of those evals are not served by the core yet, and the section says so.
 */
export function IntelligenceSection({ understanding, families, demo = false }: { understanding: Understanding | null; families: readonly Family[]; demo?: boolean }) {
  return (
    <section id="intelligence" className="panel job-band is-intelligence" aria-labelledby="intelligence-h">
      <p className="eyebrow">Job 1 · Intelligence-building</p>
      <h2 id="intelligence-h">What gtm_ai understood from Event N</h2>
      {understanding ? (
        <>
          <p className="band-lead">
            <Link href={understanding.event.href}>
              {understanding.event.source}
              {understanding.event.who ? ` from ${understanding.event.who}` : ""}, {understanding.event.when}
            </Link>
            {understanding.version ? ` moved the account state from v${understanding.version.from} to v${understanding.version.to}.` : "."}{" "}
            {understanding.trigger ?? ""}
          </p>
          {understanding.changes.length > 0 ? (
            <ul className="understood-list">
              {understanding.changes.map((c) => (
                <Change key={c.field} c={c} />
              ))}
            </ul>
          ) : (
            <p className="empty">The run's state diff records no material change.</p>
          )}
          <p className="hint">
            {understanding.signals.length > 0 ? `Signals: ${understanding.signals.join(", ")}. ` : ""}
            {understanding.bookkeeping > 0 ? `${understanding.bookkeeping} bookkeeping change${understanding.bookkeeping === 1 ? "" : "s"} not shown.` : ""}
          </p>
        </>
      ) : (
        <p className="empty">The run trace is not recorded, so what changed cannot be shown.</p>
      )}
      <div className="band-status" role="note">
        <p>
          <strong>Verdicts on this understanding are not shown here.</strong> The intelligence-building evals below judge it, and their counts for each run
          are under Results in Evals. None are guessed here.
        </p>
        {families.length > 0 ? (
          <ul className="family-chips">
            {families.map((f) => (
              <li key={f.id}>
                <Link href={withDemo(`/evals?view=catalog#family-${f.id}`, demo)}>{f.name}</Link> <span className="hint">{f.counts.live} live</span>
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </section>
  );
}
