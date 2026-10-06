import type { BucketSummary, VerdictCounts } from "@/lib/evals/gate-table";

const KEYS: readonly { key: keyof VerdictCounts; word: string; cls: string }[] = [
  { key: "fail", word: "fail", cls: "fail" },
  { key: "warn", word: "warn", cls: "warn" },
  { key: "unknown", word: "unknown", cls: "unknown" },
  { key: "pass", word: "pass", cls: "pass" },
  { key: "notMeasured", word: "not measured", cls: "nm" },
  { key: "notTriggered", word: "not triggered", cls: "nm" },
];

const signed = (n: number) => (n > 0 ? `+${n}` : String(n));

/** "+1 pass, -1 fail" for the changed counts; "no change" when every count is the same. */
export function deltaText(d: VerdictCounts): string {
  const parts = KEYS.filter((k) => d[k.key] !== 0).map((k) => `${signed(d[k.key])} ${k.word}`);
  return parts.length > 0 ? parts.join(", ") : "no change";
}

const MODE_WORD: Readonly<Record<string, string>> = { live_required: "live", live_conditional: "conditional", offline_benchmark: "offline", continuous_aggregate: "continuous" };
const BUCKET_PREFIX: Readonly<Record<string, string>> = { context: "B", decision: "D", system: "S" };

/** "5 live · 4 conditional" or "2 continuous · 4 offline": how many of the bucket's gates run in each execution mode. */
export function modeLine(bucket: string, modes: Readonly<Record<string, string>>): string {
  const mine = Object.entries(modes).filter(([id]) => id.startsWith(BUCKET_PREFIX[bucket] ?? "?"));
  return Object.entries(MODE_WORD)
    .map(([mode, word]) => ({ word, n: mine.filter(([, m]) => m === mode).length }))
    .filter((x) => x.n > 0)
    .map((x) => `${x.n} ${x.word}`)
    .join(" · ");
}

function Card({ s, modes }: { s: BucketSummary; modes: Readonly<Record<string, string>> }) {
  const total = KEYS.reduce((n, k) => n + s.counts[k.key], 0);
  return (
    <li className="sum-card" data-bucket={s.bucket}>
      <h3>{s.label}</h3>
      {total === 0 ? (
        <p className="hint">{s.bucket === "system" ? "Runs offline or continuously, not per episode" : "not measured"}</p>
      ) : (
        <>
          <div className="sum-bar" role="img" aria-label={KEYS.filter((k) => s.counts[k.key] > 0).map((k) => `${s.counts[k.key]} ${k.word}`).join(", ")}>
            {KEYS.filter((k) => s.counts[k.key] > 0).map((k) => (
              <span key={k.key} className={`sum-seg sum-${k.cls}`} style={{ flexGrow: s.counts[k.key] }} />
            ))}
          </div>
          <p className="sum-counts">
            {KEYS.filter((k) => s.counts[k.key] > 0).map((k) => (
              <span key={k.key} className={`sum-n sum-n-${k.cls}`}>{`${s.counts[k.key]} ${k.word}`}</span>
            ))}
          </p>
        </>
      )}
      {s.delta ? <p className="hint sum-delta">{`vs previous run of the same episode: ${deltaText(s.delta)}`}</p> : null}
      {modeLine(s.bucket, modes) ? <p className="hint sum-delta">{`Gates: ${modeLine(s.bucket, modes)}`}</p> : null}
    </li>
  );
}

/** One card per bucket: a stacked bar of the counts and the change against the previous run of the same episode. No aggregate score. */
export function SummaryStrip({ summaries, modes = {} }: { summaries: readonly BucketSummary[]; modes?: Readonly<Record<string, string>> }) {
  return (
    <ul className="sum-strip" aria-label="Score summary by bucket">
      {summaries.map((s) => (
        <Card key={s.bucket} s={s} modes={modes} />
      ))}
    </ul>
  );
}
