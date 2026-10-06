import { verdictWording, type CellVerdict } from "@/lib/evals/vocabulary";
import type { VerdictCounts } from "@/lib/view/run-chain";
import { VerdictIcon } from "./VerdictIcon";

const SEGMENTS: readonly { key: keyof VerdictCounts; verdict: CellVerdict; word: string; always: boolean }[] = [
  { key: "fail", verdict: "fail", word: "fail", always: true },
  { key: "warn", verdict: "warn", word: "warn", always: true },
  { key: "abstain", verdict: "abstain", word: "unsure", always: false },
  { key: "pass", verdict: "pass", word: "pass", always: true },
];

const sentence = (counts: VerdictCounts): string =>
  [...SEGMENTS.filter((s) => counts[s.key] > 0).map((s) => `${counts[s.key]} ${s.word}`), counts.notRelevant > 0 ? `${counts.notRelevant} not relevant` : null]
    .filter(Boolean)
    .join(", ") || "No evals routed";

/**
 * The verdicts of one option as segmented chips, worst first: "2 fail · 1 warn · 4 pass". Fail, warn and pass
 * always show (a zero is muted) so options line up when compared; never a score. Screen readers get one sentence.
 */
export function VerdictStrip({ counts, compact = false }: { counts: VerdictCounts; compact?: boolean }) {
  const shown = SEGMENTS.filter((s) => s.always || counts[s.key] > 0);
  return (
    <span className={`verdict-strip${compact ? " compact" : ""}`}>
      <span className="sr-only">{sentence(counts)}</span>
      <span className="strip-segments" aria-hidden="true">
        {shown.map((s) => (
          <span key={s.key} className={`strip-seg v-${s.verdict}${counts[s.key] === 0 ? " is-zero" : ""}`} title={`${counts[s.key]} ${s.word}`}>
            <VerdictIcon name={verdictWording(s.verdict).icon} />
            <span className="num">{counts[s.key]}</span>
            {compact ? null : <span className="seg-word">{s.word}</span>}
          </span>
        ))}
      </span>
      {counts.notRelevant > 0 && !compact ? (
        <span className="strip-nr" aria-hidden="true">
          {counts.notRelevant} not relevant
        </span>
      ) : null}
    </span>
  );
}
