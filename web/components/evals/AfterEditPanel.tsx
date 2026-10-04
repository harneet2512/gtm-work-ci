import type { AfterEditView } from "@/lib/evals/after-edit";
import { formatDay } from "@/lib/format";
import { WORDING, verdictWording } from "@/lib/evals/vocabulary";
import { VerdictIcon } from "./VerdictIcon";

function SendBlocked({ view }: { view: AfterEditView }) {
  if (!view.sendBlockedTitle) return null;
  return (
    <div className="send-blocked" role="note">
      <p className="send-blocked-title">
        <VerdictIcon name="x-circle" /> <span>{view.sendBlockedTitle}</span>
      </p>
      <ul>
        {view.sendBlocked.map((b) => (
          <li key={b.name}>
            <strong>{b.name}:</strong> {b.reason}
          </li>
        ))}
      </ul>
      <p className="hint">Edit the draft so it passes, or discard it. Send stays blocked until then.</p>
    </div>
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
            <p>
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
                <li key={d.evalType}>
                  <span className={`v-${d.to}`}>
                    <VerdictIcon name={verdictWording(d.to).icon} />
                  </span>{" "}
                  <span>{d.text}</span>
                </li>
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
 * the latest verdicts explains why Send is blocked.
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
      <SendBlocked view={view} />
    </section>
  );
}
