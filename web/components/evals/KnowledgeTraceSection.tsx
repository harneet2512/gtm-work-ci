import Link from "next/link";
import type { KnowledgeTrace } from "@/lib/evals/knowledge-trace";
import { formatDay } from "@/lib/format";

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/**
 * Company knowledge in this decision (retrieval is not influence). Retrieval, applicability, citation and influence
 * are separate steps and never collapse into one claim: retrieval and applicability show what the run recorded about
 * the knowledge it read (and say so when it recorded nothing), citation is what the options cite, and influence is
 * not measured, so nothing here says the knowledge changed the decision.
 */
export function KnowledgeTraceSection({ trace }: { trace: KnowledgeTrace }) {
  return (
    <section className="panel knowledge-trace" aria-labelledby="knowledge-trace-h">
      <h2 id="knowledge-trace-h">Company knowledge in this decision</h2>
      <ol className="k-steps">
        <li className={trace.attribution ? "is-done" : "is-none"}>
          <span className="k-step">Retrieved</span>
          <span>
            {trace.attribution
              ? `${plural(trace.attribution.retrieved, "piece", "pieces")} of company knowledge read as of ${formatDay(trace.attribution.asOf)}.`
              : "Not recorded: this run kept no record of the company knowledge it read."}
          </span>
        </li>
        <li className={trace.attribution ? "is-done" : "is-none"}>
          <span className="k-step">Applicable</span>
          <span>
            {trace.attribution
              ? `${trace.attribution.applicable} judged applicable${trace.attribution.exceptionBlocked > 0 ? `, ${trace.attribution.exceptionBlocked} set aside by an exception` : ""}.`
              : "Not recorded: no applicability record exists for this run."}
          </span>
        </li>
        <li className={trace.used.length > 0 ? "is-done" : "is-none"}>
          <span className="k-step">Cited</span>
          {trace.used.length > 0 ? (
            <ul>
              {trace.used.map((k) => (
                <li key={k.id}>
                  <Link href={`/knowledge/${k.id}`}>{k.label}</Link>
                  {k.status ? <span className={`badge know-${k.status}`}>{k.status}</span> : null}
                  <span className="hint"> by option{k.options.length === 1 ? "" : "s"} {k.options.join(", ")}</span>
                </li>
              ))}
            </ul>
          ) : (
            <span>No option cited company knowledge.</span>
          )}
        </li>
        <li className={trace.inChosenOption ? "is-done" : "is-none"}>
          <span className="k-step">In chosen option</span>
          <span>
            {trace.inChosenOption
              ? `Yes: the human chose an option that cites it${trace.used.some((k) => k.citedByInference) ? ", and gtm_ai's inference cites it" : ""}. This is not a measure of influence.`
              : "No: the chosen option cites no company knowledge."}
          </span>
        </li>
        <li className="is-none">
          <span className="k-step">Influence</span>
          <span>Not measured: citing knowledge is not evidence that it changed the decision.</span>
        </li>
      </ol>
      <p className="hint">
        The human's choice, edit and answer to gtm_ai's inference are recorded on this decision; a later episode reads knowledge as of its own time (see
        Replay).
      </p>
    </section>
  );
}
