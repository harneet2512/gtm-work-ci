// Human-edit recomputation (HAR-145): render the agent's draft with the human's edits resolved in
// place — the `before` span shown as invalidated and the `after` span as the recomputed replacement —
// plus whether the replacement is actually present in the artifact that was sent. Spans are derived
// from decision.edits' verbatim before/after strings; an edit whose before-text isn't found is listed
// as unlocated, never painted over the wrong text.

export interface EditSpan {
  kind: string;
  /** The draft text the human struck, when the decision recorded it. */
  before: string | null;
  /** The replacement the human wrote, when recorded. */
  after: string | null;
  /** Character offset in the original body, or -1 when the before-text can't be located. */
  at: number;
  /** True when `after` is present verbatim in the final (sent) artifact. */
  shipped: boolean | null;
}

export interface SpanBlock {
  type: "same" | "edited";
  text?: string;
  span?: EditSpan;
}

export function locateEdits(original: string | null, final: string | null, edits: readonly { kind?: string; before?: string | null; after?: string | null }[] | null | undefined): EditSpan[] {
  if (!original || !edits || edits.length === 0) return [];
  return edits.map((e) => {
    const before = e.before ?? null;
    const after = e.after ?? null;
    return {
      kind: e.kind ?? "edited",
      before,
      after,
      at: before ? original.indexOf(before) : -1,
      shipped: after == null ? null : final != null && final.includes(after),
    };
  });
}

/**
 * Splits located spans into the ones that can be painted and the ones that overlap an earlier span. Painting two
 * overlapping spans would print the shared draft text twice, so the later one is reported, never drawn. When two
 * spans start at the same offset the longer one wins.
 */
export function placeSpans(original: string, spans: readonly EditSpan[]): { painted: EditSpan[]; overlapped: EditSpan[] } {
  const located = spans.filter((s) => s.at >= 0 && s.before != null && s.at + s.before.length <= original.length).sort((a, b) => a.at - b.at || b.before!.length - a.before!.length);
  const painted: EditSpan[] = [];
  const overlapped: EditSpan[] = [];
  let cursor = 0;
  for (const s of located) {
    if (s.at < cursor) {
      overlapped.push(s);
      continue;
    }
    painted.push(s);
    cursor = s.at + s.before!.length;
  }
  return { painted, overlapped };
}

/** The draft body split into same-text blocks and edit spans, in document order; overlapping spans are not drawn. */
export function spanBlocks(original: string, spans: readonly EditSpan[]): SpanBlock[] {
  const blocks: SpanBlock[] = [];
  let cursor = 0;
  for (const s of placeSpans(original, spans).painted) {
    if (s.at > cursor) blocks.push({ type: "same", text: original.slice(cursor, s.at) });
    blocks.push({ type: "edited", span: s });
    cursor = s.at + s.before!.length;
  }
  if (cursor < original.length) blocks.push({ type: "same", text: original.slice(cursor) });
  return blocks;
}
