import type { AfterEditView } from "@/lib/evals/after-edit";
import type { SendGate } from "@/lib/evals/send-gate";
import type { SelectedView } from "@/lib/evals/selected";
import { fill, WORDING } from "@/lib/evals/vocabulary";
import type { VerdictCounts } from "@/lib/view/run-chain";
import { AfterEditPanel } from "./AfterEditPanel";
import type { SubmitDispute } from "./DisputeForm";
import { EvalLineItem } from "./EvalLineItem";
import { SendBar } from "./SendBar";
import { VerdictStrip } from "./VerdictStrip";

interface Props {
  view: SelectedView;
  letter: string;
  counts: VerdictCounts;
  after: AfterEditView;
  gate: SendGate;
  /** The human who chose another option, if any. */
  otherChooser: string | null;
  dispute: SubmitDispute;
}

/** The send status this option would meet, shown only where the after-edit panel does not already say it. */
function Gate({ gate }: { gate: SendGate }) {
  if (gate.state === "would_block" && gate.title) {
    return <SendBar state="would_block" title={gate.title} blockers={gate.blockers} hint="gtm_ai will not send this draft until it is edited so the check passes." />;
  }
  if (gate.state === "open" && gate.title) return <SendBar state="open" title={gate.title} blockers={[]} />;
  return null;
}

/**
 * The evals of one option, worst first (spec Level 2 at web depth): who chose it, what Send would do, what the
 * edit changed, then one card per verdict with Not relevant collapsed.
 */
export function SelectedOption({ view, letter, counts, after, gate, otherChooser, dispute }: Props) {
  return (
    <section id="selected" className="panel selected-evals" aria-labelledby="selected-h">
      <div className="selected-head">
        <div>
          <p className="eyebrow">Option {letter}</p>
          <h2 id="selected-h">{`Evals for option ${letter}: ${view.title}`}</h2>
          <p className="option-marks">
            {view.isGhostPick ? <span className="mark ghost-pick">{WORDING.phrases.ghost_pick}</span> : null}
            {view.isChosen && view.chosenBy ? <span className="mark chosen">{fill("human_choice", { actor: view.chosenBy })}</span> : null}
            {!view.isChosen && otherChooser ? <span className="hint">This is not the option {otherChooser} chose.</span> : null}
          </p>
        </div>
        <VerdictStrip counts={counts} />
      </div>
      {view.held ? <p className="held">{view.held}</p> : null}
      <AfterEditPanel view={after} />
      <Gate gate={gate} />
      {view.lines.length === 0 ? (
        <p className="empty">No eval returned a verdict for this option.</p>
      ) : (
        <ol className="eval-lines">
          {view.lines.map((line) => (
            <EvalLineItem key={line.resultId} line={line} dispute={dispute} />
          ))}
        </ol>
      )}
      {view.notRelevant.length > 0 ? (
        <details className="not-relevant">
          <summary>{fill("not_relevant_summary", { count: view.notRelevant.length })}</summary>
          <ul>
            {view.notRelevant.map((n) => (
              <li key={n.name}>
                <strong>{n.name}</strong>: {n.why}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}
