import { firstParam } from "@/lib/params";
import { getExplorer } from "@/lib/cached-loaders";
import { evalName, verdictWording, type CellVerdict } from "@/lib/evals/vocabulary";

export const dynamic = "force-dynamic";

type Search = Promise<Record<string, string | string[] | undefined>>;

/**
 * The eval-result inspector (HAR-145): `?result=<id>` on /evals?view=results shows the full EvalResult
 * — verdict, reason, grader/kind, evidence and state/activity/knowledge refs — verbatim from core.
 */
export default async function EvalsInspector({ searchParams }: { searchParams: Search }) {
  const sp = await searchParams;
  const resultId = firstParam(sp.result) ?? null;
  if (!resultId || firstParam(sp.view) !== "results") {
    return (
      <div className="inspector-pane">
        <h2>Inspector</h2>
        <p className="hint">Select a result row to inspect its verdict, evidence and refs.</p>
      </div>
    );
  }

  const { byId } = await getExplorer();
  const r = byId.get(resultId) ?? null;
  if (!r) {
    return (
      <div className="inspector-pane">
        <h2>Inspector</h2>
        <p className="hint">No result {resultId.slice(0, 13)}… on the loaded runs.</p>
      </div>
    );
  }

  const ids = (label: string, list: readonly string[] | undefined | null) =>
    list && list.length > 0 ? (
      <p className="hint mono">
        {label}: {list.map((id) => `${id.slice(0, 13)}…`).join("  ")}
      </p>
    ) : null;
  // evidence_refs are quoted snippets ({activity_id, quote, …}), not id lists — show the quote.
  const evidence = (r.evidence_refs ?? []).map((e) => e.quote ?? e.activity_id);

  return (
    <div className="inspector-pane">
      <h2>{evalName(r.eval_type)}</h2>
      <p className={`insp-status st-${r.verdict}`}>
        {verdictWording(r.verdict as CellVerdict).label}
        {r.blocking ? " · blocks send" : ""}
      </p>
      {r.reason ? <p>{r.reason}</p> : null}
      <p className="hint mono">
        {r.eval_version} · {r.kind ?? "?"} · {r.evidence_class?.replace("_", " ") ?? "?"} · {r.model ?? "no model"}
      </p>
      {r.score != null ? <p className="hint mono">score {r.score} · confidence {r.confidence ?? "—"}</p> : null}
      {r.suggested_correction ? <p className="hint">Suggested: {r.suggested_correction}</p> : null}
      {ids("state", r.state_refs)}
      {ids("activities", r.activity_refs)}
      {ids("knowledge", r.knowledge_refs)}
      {evidence.length > 0 ? (
        <details>
          <summary>Evidence ({evidence.length})</summary>
          <ul className="insp-evidence">
            {evidence.map((q, i) => (
              <li key={i}>“{q}”</li>
            ))}
          </ul>
        </details>
      ) : null}
      {r.diagnostics != null ? (
        <details>
          <summary>Diagnostics</summary>
          <pre>{JSON.stringify(r.diagnostics, null, 2)}</pre>
        </details>
      ) : null}
    </div>
  );
}
