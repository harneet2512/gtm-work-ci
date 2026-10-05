import Link from "next/link";
import type { ExplorerData } from "@/lib/load-eval-results";
import { compareRuns } from "@/lib/view/evals-explorer";
import type { CellVerdict } from "@/lib/evals/vocabulary";
import { ScrollTable } from "@/components/ScrollTable";

const MARK: Record<CellVerdict, string> = { fail: "✗", warn: "!", abstain: "◌", pass: "✓", not_relevant: "·", not_checked: "—" };
const DELTA_MARK = { same: "=", changed: "≠", improved: "↑", regressed: "↓", "one-sided": "◐" } as const;

const improvedOrRegressed = (rows: { delta: string }[]) => rows.filter((r) => r.delta === "improved" || r.delta === "regressed").length;

const short = (id: string) => (id.length > 13 ? `${id.slice(0, 13)}…` : id);

/**
 * Two runs side by side on the same eval axes (HAR-145 run comparison). Cells carry the run's worst
 * verdict per eval type; "—" means that run never produced the check — distinct from a pass.
 */
export function RunCompare({ data, a, b }: { data: ExplorerData; a: string | null; b: string | null }) {
  const eligible = data.runs;
  if (eligible.length < 2) {
    return <p className="hint">Two or more runs with eval results are needed to compare — {eligible.length} found.</p>;
  }
  const ra = a && eligible.some((r) => r.id === a) ? a : eligible[0]!.id;
  const rb = b && eligible.some((r) => r.id === b) ? b : eligible[1]!.id;
  const rows = compareRuns(data.rows, ra, rb);
  const changed = rows.filter((r) => r.delta !== "same").length;
  const href = (x: string, y: string) => `/evals?view=compare&runs=${x},${y}`;

  return (
    <div className="compare">
      <div className="compare-heads">
        {([["Run A", ra], ["Run B", rb]] as const).map(([label, id]) => {
          const meta = eligible.find((r) => r.id === id)!;
          return (
            <div key={label} className="cmp-side">
              <span className="eyebrow">{label}</span>
              <code>{short(id)}</code>
              <span className="hint">
                {meta.phase ?? "—"} · {meta.rows} results
              </span>
            </div>
          );
        })}
        <nav className="chipset" aria-label="Compare pairs">
          {eligible.slice(0, 4).flatMap((x, i) =>
            eligible.slice(i + 1, 4).map((y) => (
              <Link key={`${x.id}-${y.id}`} className={`chip ${x.id === ra && y.id === rb ? "on" : ""}`} href={href(x.id, y.id)}>
                {short(x.id)}×{short(y.id)}
              </Link>
            )),
          )}
        </nav>
      </div>
      <p className="hint">
        {rows.length} eval types · {rows.filter((r) => r.delta === "improved").length} improved · {rows.filter((r) => r.delta === "regressed").length} regressed ·{" "}
        {changed - improvedOrRegressed(rows)} otherwise differ · {rows.length - changed} unchanged
      </p>
      <ScrollTable label="Run comparison"><table className="dense">
        <thead>
          <tr>
            <th>Eval</th>
            <th className="mono">{short(ra)}</th>
            <th className="mono">{short(rb)}</th>
            <th aria-label="Delta" />
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.evalType} className={`cmp-${r.delta}`}>
              <td>
                {r.name}
                <span className="etype mono">{r.evalType}</span>
              </td>
              <td className={`mono v-${r.a}`}>{MARK[r.a]} {r.a.replace("_", " ")}</td>
              <td className={`mono v-${r.b}`}>{MARK[r.b]} {r.b.replace("_", " ")}</td>
              <td className="mono" aria-label={r.delta}>
                {DELTA_MARK[r.delta]}
              </td>
            </tr>
          ))}
        </tbody>
      </table></ScrollTable>
    </div>
  );
}
