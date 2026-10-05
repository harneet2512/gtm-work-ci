import type { HumanStrategyDecision, RunStrategies } from "@/lib/api/types";
import { locateEdits, placeSpans, spanBlocks } from "@/lib/view/edit-spans";

const KIND_WORDS: Record<string, string> = {
  cta_changed: "CTA changed",
  paragraph_edited: "paragraph edited",
};

/**
 * What the human's edit invalidated and what was recomputed (HAR-145): the draft rendered with struck
 * before-spans replaced by the shipped after-spans — each tagged with its edit kind and whether the
 * replacement is verbatim in the sent artifact. Edits that can't be located list under the body.
 */
export function EditSpans({ decision, strategies }: { decision: HumanStrategyDecision; strategies: RunStrategies | null }) {
  const candidate = strategies?.strategy_set?.candidates?.find((c) => c.candidate_id === decision.selected_candidate_id) ?? null;
  const original = (candidate?.full_action_artifact as { body?: string } | null)?.body ?? null;
  const final = (decision.final_artifact as { body?: string } | null)?.body ?? null;
  const edits = (decision.edits ?? []) as { kind?: string; before?: string | null; after?: string | null }[];
  if (edits.length === 0 || !original) return null;

  const spans = locateEdits(original, final, edits);
  const blocks = spanBlocks(original, spans);
  const { overlapped } = placeSpans(original, spans);
  const lost = spans.filter((s) => s.at < 0);

  return (
    <section className="panel edit-spans" aria-labelledby="edit-spans-h">
      <h2 id="edit-spans-h">What the edit changed</h2>
      <p className="hint">
        Struck spans are what the human invalidated; the highlighted replacement is what was sent.
        {spans.every((s) => s.shipped !== false) ? " Every located edit is present in the sent artifact." : " A span not in the sent artifact is flagged."}
      </p>
      <div className="draft-diff" role="document">
        {blocks.map((b, i) =>
          b.type === "same" ? (
            <span key={i}>{b.text}</span>
          ) : (
            <span key={i} className="espan">
              {b.span!.before ? <del>{b.span!.before}</del> : null}
              {b.span!.after ? <ins>{b.span!.after}</ins> : null}
              <span className="ekind">
                {KIND_WORDS[b.span!.kind] ?? b.span!.kind}
                {b.span!.shipped === false ? " — not in sent artifact" : ""}
              </span>
            </span>
          ),
        )}
      </div>
      {lost.length > 0 ? (
        <p className="hint warn">
          {lost.length} recorded edit{lost.length === 1 ? "" : "s"} could not be located in the draft text ({lost.map((s) => s.kind).join(", ")}) — shown unmarked rather than painted over the wrong words.
        </p>
      ) : null}
      {overlapped.length > 0 ? (
        <p className="hint">
          {overlapped.length} edit{overlapped.length === 1 ? "" : "s"} overlap{overlapped.length === 1 ? "s" : ""} an earlier edit's text ({overlapped.map((s) => s.kind).join(", ")}) and {overlapped.length === 1 ? "is" : "are"} not drawn, to avoid repeating the draft.
        </p>
      ) : null}
    </section>
  );
}
