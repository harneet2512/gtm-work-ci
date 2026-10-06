import type { VerdictCounts as Counts } from "@/lib/view/run-chain";
import { verdictWording, type CellVerdict } from "@/lib/evals/vocabulary";
import { VerdictIcon } from "./VerdictIcon";

/**
 * The one verdict mark every eval surface uses (eval design spec 4b-3: same vocabulary, order and icons as
 * Slack): an icon plus the word. Color is never the only signal; with `iconOnly` the word stays for screen
 * readers.
 */
export function Verdict({ verdict, iconOnly = false }: { verdict: CellVerdict; iconOnly?: boolean }) {
  const w = verdictWording(verdict);
  return (
    <span className={`verdict v-${verdict}`}>
      <VerdictIcon name={w.icon} />
      <span className={iconOnly ? "sr-only" : "verdict-word"}>{w.label}</span>
    </span>
  );
}

const ORDER: readonly { key: keyof Counts; verdict: CellVerdict; word: string }[] = [
  { key: "fail", verdict: "fail", word: "fail" },
  { key: "warn", verdict: "warn", word: "warn" },
  { key: "abstain", verdict: "abstain", word: "unsure" },
  { key: "pass", verdict: "pass", word: "pass" },
  { key: "notRelevant", verdict: "not_relevant", word: "not relevant" },
];

/** "2 fail · 1 pass · 1 not relevant": counts side by side, never a score. Zero counts are left out. */
export function VerdictCounts({ counts }: { counts: Counts }) {
  const shown = ORDER.filter((o) => counts[o.key] > 0);
  if (shown.length === 0) return <p className="verdict-counts empty">No evals routed.</p>;
  return (
    <p className="verdict-counts">
      {shown.map((o) => (
        <span key={o.key} className={`count v-${o.verdict}`}>
          <VerdictIcon name={verdictWording(o.verdict).icon} />
          {`${counts[o.key]} ${o.word}`}
        </span>
      ))}
    </p>
  );
}
