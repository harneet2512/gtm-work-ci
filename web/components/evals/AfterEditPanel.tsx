import type { AfterEditView, VerdictDelta } from "@/lib/evals/after-edit";
import { formatDay } from "@/lib/format";
import { WORDING, verdictWording, type CellVerdict } from "@/lib/evals/vocabulary";
import { SendBar } from "./SendBar";
import { VerdictIcon } from "./VerdictIcon";

function Pill({ verdict }: { verdict: CellVerdict }) {
  const w = verdictWording(verdict);
  return (
    <span className={`delta-pill v-${verdict}`}>
      <VerdictIcon name={w.icon} />
      {w.label}
    </span>
  );
}

/** "CTA calibration  [Warn] → [Pass]  after your edit": the sentence for screen readers, the pills for the eye. */
function DeltaChip({ d }: { d: VerdictDelta }) {
  return (
    <li className={`delta-chip to-${d.to}`}>
      <span className="sr-only">{d.text}</span>
      <span className="delta-visual" aria-hidden="true">
        <span className="delta-name">{d.name}</span>
        <Pill verdict={d.from} />
        <span className="delta-arrow">→</span>
        <Pill verdict={d.to} />
        <span className="delta-after">after your edit</span>
      </span>
    </li>
  );
}

const sentLine = (view: AfterEditView) => (view.sent ? `Sent${view.sent.at ? ` ${formatDay(view.sent.at)}` : ""}.` : null);

function Body({ view }: { view: AfterEditView }) {
  switch (view.state) {
    case "no_decision":
      return <p>Nobody has chosen an option yet. When someone edits the chosen draft, Ghost re-checks it before it can be sent.</p>;
    case "not_chosen":
      return null;
    case "unedited":
      return <p>{view.sent ? "Sent as drafted: no edits, so these verdicts are the final ones." : "Chosen without edits: these verdicts are for the draft as it stands."}</p>;
    case "not_reevaluated":
      return (
        <>
          <p>{view.summary}</p>
          <p>These verdicts were given on Ghost's draft, not on the edited version, and the core has not re-run them.</p>
          {view.standing.length > 0 ? (
            <p className="standing-line">
              Until they are re-run, these still stand:{" "}
              {view.standing.map((s, i) => (
                <span key={s.name}>
                  {i > 0 ? ", " : ""}
                  {verdictWording(s.verdict).label} on {s.name}
                </span>
              ))}
              .
            </p>
          ) : null}
        </>
      );
    case "reevaluated":
      return (
        <>
          {view.summary ? <p>{view.summary}</p> : null}
          {view.deltas.length > 0 ? (
            <ul className="deltas">
              {view.deltas.map((d) => (
                <DeltaChip key={d.evalType} d={d} />
              ))}
            </ul>
          ) : (
            <p>No verdict changed after the edit.</p>
          )}
          {view.unchanged > 0 ? <p className="hint">{view.unchanged === 1 ? "1 other check" : `${view.unchanged} other checks`} unchanged.</p> : null}
          {view.evaluatedAt ? <p className="hint">Re-checked {formatDay(view.evaluatedAt)}.</p> : null}
        </>
      );
  }
}

const TITLES: Readonly<Record<AfterEditView["state"], string>> = {
  no_decision: "After an edit",
  not_chosen: "After an edit",
  unedited: "After an edit",
  not_reevaluated: WORDING.phrases.not_reevaluated,
  reevaluated: "After the edit",
};

/**
 * What happened to the verdicts after the human's edit (spec: re-run the affected evals before Send and show what
 * changed). With no send-time results it says "Not re-evaluated yet", never a guessed delta; a blocking failure on
 * the latest verdicts locks Send and says why and how to unlock it.
 */
export function AfterEditPanel({ view }: { view: AfterEditView }) {
  if (view.state === "not_chosen") return null;
  const headingId = `after-edit-${view.state}`;
  return (
    <section className={`after-edit after-${view.state}`} aria-labelledby={headingId}>
      <h3 id={headingId}>
        {view.state === "not_reevaluated" ? <VerdictIcon name="dashed-circle" /> : null}
        <span>{TITLES[view.state]}</span>
      </h3>
      <Body view={view} />
      {view.discarded ? <p>Discarded: nothing was sent.</p> : null}
      {view.state !== "unedited" && sentLine(view) ? <p className="hint">{sentLine(view)}</p> : null}
      {view.sendBlockedTitle ? (
        <SendBar state="blocked" title={view.sendBlockedTitle} blockers={view.sendBlocked} hint="Edit the draft so it passes, or discard it. Send stays blocked until then." />
      ) : null}
    </section>
  );
}
