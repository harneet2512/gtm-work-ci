// The compact Judgment Delta: a word diff of one edit reduced to the changed words and a little context, so the hero shows
// what changed and not the whole paragraph. The full before and after stay available for a "Show full edit" toggle.

export const MAX_DIFF_CHARS = 160;
const CONTEXT_WORDS = 5;

export type DiffKind = "same" | "del" | "ins";
export interface DiffSegment {
  kind: DiffKind;
  text: string;
}
export interface CompactDiff {
  segments: DiffSegment[];
  /** True when words outside the shown window were left out. */
  truncated: boolean;
  full: { before: string; after: string };
}

const words = (s: string): string[] => s.split(/(\s+)/).filter((w) => w !== "");
const join = (w: readonly string[]) => w.join("");

function clip(s: string, max: number): { text: string; cut: boolean } {
  return s.length <= max ? { text: s, cut: false } : { text: `${s.slice(0, Math.max(0, max - 1)).trimEnd()}…`, cut: true };
}

export function compactDiff(before: string, after: string): CompactDiff {
  const full = { before, after };
  if (before === after) return { segments: [], truncated: false, full };
  const a = words(before);
  const b = words(after);
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }
  const lead = a.slice(0, start);
  const trail = a.slice(endA);
  // Context is counted in words (whitespace tokens included, so double it).
  const ctx = CONTEXT_WORDS * 2;
  const pre = lead.slice(Math.max(0, lead.length - ctx));
  const post = trail.slice(0, ctx);
  let truncated = pre.length < lead.length || post.length < trail.length;

  let del = join(a.slice(start, endA));
  let ins = join(b.slice(start, endB));
  const room = Math.max(40, MAX_DIFF_CHARS - join(pre).length - join(post).length);
  const half = Math.floor(room / 2);
  const d = clip(del, half);
  const i = clip(ins, half);
  del = d.text;
  ins = i.text;
  truncated = truncated || d.cut || i.cut;

  const segments: DiffSegment[] = [];
  const push = (kind: DiffKind, text: string) => text !== "" && segments.push({ kind, text });
  push("same", (pre.length < lead.length ? "… " : "") + join(pre));
  push("del", del);
  push("ins", ins);
  push("same", join(post) + (post.length < trail.length ? " …" : ""));
  return { segments, truncated, full };
}
