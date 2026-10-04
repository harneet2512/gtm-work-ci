import Link from "next/link";
import type { Knowledge } from "@/lib/api/types";
import { formatUtc, shortId } from "@/lib/format";
import { knowledgeLine, type WhyChanged } from "@/lib/view/run-chain";
import { evalName, type BundleVerdict } from "@/lib/evals/vocabulary";
import { Verdict } from "@/components/evals/Verdict";

const VERDICTS: readonly string[] = ["fail", "warn", "abstain", "pass", "not_relevant"];
/** Inference verdicts are bundle verdicts by contract; anything else reads as not checked, never as a pass. */
const asVerdict = (v: string): BundleVerdict | "not_checked" => (VERDICTS.includes(v) ? (v as BundleVerdict) : "not_checked");

function KnowledgeSide({ label, ids, knowledge }: { label: string; ids: readonly string[]; knowledge: Record<string, Knowledge | null> }) {
  return (
    <div className="knowledge-side">
      <h4>{label}</h4>
      {ids.length === 0 ? (
        <p className="empty">No knowledge applied.</p>
      ) : (
        <ul className="knowledge">
          {ids.map((id) => {
            const line = knowledgeLine(id, knowledge[id]);
            return (
              <li key={id}>
                <Link href={`/knowledge/${id}`}>{line.label}</Link>
                {line.status ? <span className={`badge know-${line.status}`}> {line.status}</span> : null}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

/**
 * The §16 panel: why did this action change? It answers with what was recorded — the candidate Ghost
 * ranked first, the one the human chose, the literal edits, the eval-verdict differences between the
 * two sides, the semantic delta and the knowledge each side applied. With no judgment inference the
 * panel is explicit that only the knowledge-application comparison is shown; it never fabricates a
 * counterfactual draft.
 */
export function WhyChangedPanel({ why, knowledge }: { why: WhyChanged; knowledge: Record<string, Knowledge | null> }) {
  return (
    <section aria-labelledby="why-h" className="panel why-panel">
      <h2 id="why-h">Why did this action change?</h2>
      {!why.decided ? (
        <p className="empty">No human decision yet — this panel fills in when someone chooses among the candidates.</p>
      ) : (
        <>
          <p className="summary">
            {why.agreed ? (
              <>
                The human went with Ghost's pick{why.preferredTitle ? <> — “{why.preferredTitle}”</> : null}.
              </>
            ) : (
              <>
                Ghost preferred “{why.preferredTitle ?? "unknown"}”; the human chose “{why.chosenTitle ?? "unknown"}”.
              </>
            )}
          </p>
          {why.candidateDifferences.length > 0 ? (
            <ul className="hint">
              {why.candidateDifferences.map((d, i) => (
                <li key={i}>{d}</li>
              ))}
            </ul>
          ) : null}
          {why.evalDifferences.length > 0 ? (
            <>
              <h4>Eval verdicts that differ</h4>
              <ul className="eval-diffs">
                {why.evalDifferences.map((d) => (
                  <li key={d.evalType}>
                    <strong>{evalName(d.evalType)}</strong>: Ghost's pick <Verdict verdict={asVerdict(d.preferredVerdict)} /> → chosen <Verdict verdict={asVerdict(d.chosenVerdict)} />
                    {d.note ? <span className="hint"> — {d.note}</span> : null}
                  </li>
                ))}
              </ul>
            </>
          ) : null}
          {why.statement ? (
            <div className="semantic-delta">
              <h4>Semantic delta</h4>
              <p className="summary">{why.statement}</p>
              {why.semanticLabels.length > 0 ? <p className="hint">labels: {why.semanticLabels.join(", ")}</p> : null}
              {why.evidenceRefs.length > 0 ? (
                <ul className="evidence-list">
                  {why.evidenceRefs.map((r, i) => (
                    <li key={i} className="evidence">
                      {r.quote ? <blockquote>{r.quote}</blockquote> : null}
                      <cite>
                        activity <code title={r.activity_id}>{shortId(r.activity_id)}</code>
                        {r.speaker_person_id ? (
                          <>
                            {" "}
                            · speaker <code title={r.speaker_person_id}>{shortId(r.speaker_person_id)}</code>
                          </>
                        ) : null}
                        {r.occurred_at ? <> · {formatUtc(r.occurred_at)}</> : null}
                      </cite>
                    </li>
                  ))}
                </ul>
              ) : null}
              {why.correctedStatement ? (
                <p className="hint">
                  corrected by the human{why.humanVerdict ? ` (${why.humanVerdict})` : ""}: {why.correctedStatement}
                </p>
              ) : why.humanVerdict && why.humanVerdict !== "pending" ? (
                <p className="hint">the human {why.humanVerdict} this reading{why.humanNote ? ` — ${why.humanNote}` : ""}</p>
              ) : null}
            </div>
          ) : null}
        </>
      )}
      <div className="knowledge-compare">
        <KnowledgeSide label={why.decided && !why.agreed ? "Knowledge on Ghost's pick" : "Knowledge applied"} ids={why.preferredKnowledge} knowledge={knowledge} />
        {why.decided && !why.agreed ? <KnowledgeSide label="Knowledge on the chosen action" ids={why.chosenKnowledge} knowledge={knowledge} /> : null}
      </div>
      {why.noApplicableKnowledge ? <p className="hint">Ghost recorded that no company knowledge applied to this decision.</p> : null}
      {why.comparisonOnly ? <p className="hint">No judgment inference recorded yet — this is the knowledge-application comparison only, not a counterfactual.</p> : null}
    </section>
  );
}
