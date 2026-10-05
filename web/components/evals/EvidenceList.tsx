import Link from "next/link";
import type { EvidenceSnippet } from "@/lib/evals/evidence";

/** "Open the email", "Open the meeting": the link names the source, never an id. */
const openLabel = (source: string | null) => (source ? `Open the ${source.toLowerCase()}` : "Open the source");

/**
 * The evidence behind one verdict (spec: "[evidence] opens the source snippet: who, when, what was said"). Each
 * quote is attributed and dated from the recorded trace and links to its activity in the run trace. A ref the
 * trace does not hold says so instead of inventing a name or a link.
 */
export function EvidenceList({ snippets }: { snippets: readonly EvidenceSnippet[] }) {
  if (snippets.length === 0) return <p className="empty">This verdict cites no evidence.</p>;
  return (
    <ul className="evidence-snippets">
      {snippets.map((s, i) => (
        <li key={`${s.activityId}-${i}`}>
          {s.quote ? <blockquote>“{s.quote}”</blockquote> : <p className="empty">No quote recorded for this source.</p>}
          <p className="attribution">
            {s.who ? (
              <>
                <strong>{s.who}</strong>
                {s.whoTitle ? `, ${s.whoTitle}` : ""}
              </>
            ) : (
              <span>Speaker not recorded</span>
            )}
            {s.source ? ` · ${s.source}` : ""}
            {s.when ? <> · <time>{s.when}</time></> : ""}
          </p>
          {s.summary ? <p className="hint">{s.summary}</p> : null}
          {s.href ? (
            <Link href={s.href} className="evidence-link">
              {openLabel(s.source)}
            </Link>
          ) : (
            <p className="hint">The source activity is not in this run's trace.</p>
          )}
        </li>
      ))}
    </ul>
  );
}
