// The three-candidate decision screen (HAR-149 section 4): the options side by side, each judged per criterion, then why the
// recommendation won, what the human did and, if they edited, what was recomputed. Server component: nothing here is interactive.
import { ResultPill } from "./DrawerSections";
import type { CandidateCard, DecisionScreen, GateLine } from "@/lib/evals/inspector/candidates";

const MAX_EVIDENCE = 2;

function Card({ c }: { c: CandidateCard }) {
  return (
    <article className="opt" data-letter={c.letter} data-recommended={c.recommended} data-chosen={c.chosen} data-blocked={c.blocked}>
      <header>
        <span className="opt-letter">{`Option ${c.letter}`}</span>
        <span className="opt-tags">
          {c.recommended ? <span className="tag tag-reco">Recommended</span> : null}
          {c.chosen ? <span className="tag tag-chosen">Chosen</span> : null}
          {c.blocked ? <span className="tag tag-blocked">Blocked by a check</span> : null}
        </span>
      </header>
      <h2>{c.title}</h2>
      <p className="opt-strategy">{`${c.strategy} · ${c.actionType}`}</p>
      <p className="opt-intent">{c.intent}</p>

      <h3>Judged on</h3>
      {c.verdicts.overall === null ? (
        <p className="hint">{c.verdicts.note}</p>
      ) : (
        <ul className="opt-grid">
          {c.verdicts.rows.map((r, n) => (
            <li key={`${r.id}-${n}`} data-result={r.result}>
              <ResultPill result={r.result} />
              <span className="opt-crit">{r.label}</span>
              {r.result !== "pass" && r.why ? <span className="opt-why">{r.why}</span> : null}
            </li>
          ))}
        </ul>
      )}
      {c.verdicts.note && c.verdicts.overall !== null ? <p className="dr-aside">{c.verdicts.note}</p> : null}

      <h3>Uses this evidence</h3>
      {c.evidence.length === 0 ? <p className="hint">It cites no evidence.</p> : null}
      <ul className="opt-evidence">
        {c.evidence.slice(0, MAX_EVIDENCE).map((e, n) => (
          <li key={`${e.id}-${n}`}>{e.text ? <blockquote>{e.text}</blockquote> : <span className="hint">The text of this record is not loaded here.</span>}</li>
        ))}
      </ul>
      {c.evidence.length > MAX_EVIDENCE ? <p className="hint">{`and ${c.evidence.length - MAX_EVIDENCE} more`}</p> : null}

      <h3>Uses this company knowledge</h3>
      {c.knowledge.length === 0 ? <p className="hint">None applied.</p> : <ul className="opt-knowledge">{c.knowledge.map((k) => <li key={k.id}>{k.title ?? "A piece of company knowledge"}</li>)}</ul>}

      <p className="opt-rank">{`Ranked ${c.rank} of 3`}</p>
    </article>
  );
}

function Line({ label, g }: { label: string; g: GateLine | null }) {
  if (!g) return <p className="hint">{`${label}: no result is stored yet.`}</p>;
  return (
    <p className="dec-line">
      <ResultPill result={g.verdict} />
      <span>
        <strong>{label}</strong>
        {g.label ? ` · ${g.label}` : ""}
        {g.why ? `: ${g.why}` : ""}
      </span>
    </p>
  );
}

export function DecisionView({ screen }: { screen: DecisionScreen }) {
  if (screen.cards.length === 0) return <p className="hint">No options are stored for this episode, so there is nothing to compare.</p>;
  const w = screen.whyWon;
  return (
    <div className="decision">
      <div className="opts" data-testid="candidates">
        {screen.cards.map((c) => (
          <Card key={c.id} c={c} />
        ))}
      </div>

      {w ? (
        <section className="dec-section" data-testid="why-won">
          <h2>{w.heading}</h2>
          {w.missing ? <p className="hint">{w.missing}</p> : null}
          <ul className="dec-reasons">
            {w.reasons.map((r) => (
              <li key={`${r.higher}${r.lower}`}>
                <span className="dec-pair">{`Option ${r.higher} above Option ${r.lower}`}</span>
                <span>{r.text}</span>
              </li>
            ))}
          </ul>
          {w.abstained ? <p className="dr-aside">gtm_ai did not recommend a move: the top-ranked option is a quiet one (wait, note or no action).</p> : null}
          {w.d3 ? (
            <>
              <p className="dec-line">
                <ResultPill result={w.d3.verdict} />
                <span>
                  <strong>Was the ranking right?</strong> {w.d3.why}
                </span>
              </p>
              <ul className="opt-grid dec-d3">
                {w.d3.criteria.map((r, n) => (
                  <li key={`${r.id}-${n}`}>
                    <ResultPill result={r.result} />
                    <span className="opt-crit">{r.label}</span>
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <p className="hint">The ranking itself has not been judged: no D3 result is stored.</p>
          )}
        </section>
      ) : null}

      {screen.human ? (
        <section className="dec-section" data-testid="human-choice">
          <h2>{screen.human.choseDifferent ? "The human chose differently" : "The human's choice"}</h2>
          <p className="dec-headline">{screen.human.headline}</p>
          <Line label="What the choice says about the recommendation" g={screen.human.d4} />
          {screen.human.interpretation ? (
            <p className="dec-interp">
              <span className="dr-key">gtm_ai's reading</span> {screen.human.interpretation}
              {screen.human.classes.length > 0 ? <span className="hint">{` (${screen.human.classes.join(", ")}${screen.human.signal ? `; ${screen.human.signal} signal` : ""})`}</span> : null}
            </p>
          ) : null}
        </section>
      ) : null}

      {screen.edit ? (
        <section className="dec-section" data-testid="human-edit">
          <h2>What the edit changed</h2>
          <Line label="What the edit means" g={screen.edit.d5} />
          <Line label="Did everything that depended on it recompute?" g={screen.edit.d7} />
          {screen.edit.recomputation ? (
            <div className="dec-recompute">
              <p className="hint">{screen.edit.recomputation.headline}</p>
              {screen.edit.recomputation.entries.map((e) => (
                <div key={e.index} className="dec-entry">
                  <p className="dec-pair">{e.edit}</p>
                  <div className="dec-cols">
                    <div>
                      <h4>Became stale</h4>
                      <ul>{e.invalidated.length === 0 ? <li className="hint">nothing</li> : e.invalidated.map((r) => <li key={r.key}>{r.label}</li>)}</ul>
                    </div>
                    <div>
                      <h4>Recomputed</h4>
                      <ul>{e.reevaluated.length === 0 ? <li className="hint">nothing</li> : e.reevaluated.map((r) => <li key={r.key}>{`${r.label}${r.verdict ? ` (${r.verdict})` : ""}`}</li>)}</ul>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="hint">What the edit invalidated and recomputed is not available for this episode.</p>
          )}
          {screen.edit.d8 ? (
            <p className="dec-line">
              <ResultPill result={screen.edit.d8.verdict} />
              <span>
                <strong>The message that went out, checked again</strong>
                {screen.edit.d8.recomputed ? ` · re-run after the edit${screen.edit.d8.previousVerdict ? ` (it said ${screen.edit.d8.previousVerdict.toUpperCase()} before)` : ""}` : ""}
                {screen.edit.d8.why ? `: ${screen.edit.d8.why}` : ""}
              </span>
            </p>
          ) : (
            <p className="hint">The final message has not been checked: no D8 result is stored.</p>
          )}
        </section>
      ) : null}

      <p className="dec-note">{screen.scoresNote}</p>
    </div>
  );
}
