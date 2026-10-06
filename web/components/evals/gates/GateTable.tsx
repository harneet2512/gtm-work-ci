"use client";

import { columnVisibilityFeature, createColumnHelper, createSortedRowModel, rowSortingFeature, sortFn_alphanumeric, tableFeatures, useTable, type SortingState, type ColumnVisibilityState } from "@tanstack/react-table";
import { useEffect, type KeyboardEvent } from "react";
import { compareRows, type GateRow } from "@/lib/evals/gate-table";
import { formatUtc } from "@/lib/format";
import { VerdictPill } from "./VerdictPill";

const features = tableFeatures({ columnVisibilityFeature, rowSortingFeature, sortedRowModel: createSortedRowModel(), sortFns: { alphanumeric: sortFn_alphanumeric } });
const helper = createColumnHelper<typeof features, GateRow>();

const NM = "not measured";
const text = (a: string | null | undefined) => a ?? "";

/** Column ids in display order, with the label the Display menu shows; "n" and "gate" always stay. */
export const COLUMNS: readonly { id: string; label: string; locked?: boolean }[] = [
  { id: "n", label: "#", locked: true },
  { id: "gate", label: "Gate", locked: true },
  { id: "episode", label: "Episode" },
  { id: "verdict", label: "Verdict", locked: true },
  { id: "observed", label: "Observed" },
  { id: "why", label: "Why" },
  { id: "evidence", label: "Evidence" },
  { id: "grader", label: "Grader" },
  { id: "calibrated", label: "Calibrated" },
  { id: "time", label: "Time" },
];

const byText = (pick: (r: GateRow) => string) => (a: { original: GateRow }, b: { original: GateRow }) => pick(a.original).localeCompare(pick(b.original)) || compareRows(a.original, b.original);

const columns = helper.columns([
  helper.display({ id: "n", header: "#", enableSorting: false }),
  helper.accessor((r) => r.gateOrder, { id: "gate", header: "Gate", sortFn: (a, b) => a.original.gateOrder - b.original.gateOrder || compareRows(a.original, b.original) }),
  helper.accessor((r) => r.episodeLabel, { id: "episode", header: "Episode", sortFn: byText((r) => r.episodeLabel) }),
  helper.accessor((r) => r.verdict, { id: "verdict", header: "Verdict", sortFn: (a, b) => compareRows(a.original, b.original) }),
  helper.accessor((r) => text(r.observed), { id: "observed", header: "Observed", sortFn: byText((r) => text(r.observed)) }),
  helper.accessor((r) => r.why, { id: "why", header: "Why", sortFn: byText((r) => r.why) }),
  helper.accessor((r) => r.evidence.length, { id: "evidence", header: "Evidence", sortFn: (a, b) => a.original.evidence.length - b.original.evidence.length || compareRows(a.original, b.original) }),
  helper.accessor((r) => text(r.graderKind), { id: "grader", header: "Grader", sortFn: byText((r) => text(r.graderKind)) }),
  helper.accessor((r) => r.calibrated, { id: "calibrated", header: "Calibrated", enableSorting: false }),
  helper.accessor((r) => text(r.time), { id: "time", header: "Time", sortFn: byText((r) => text(r.time)) }),
]);

function cellOf(id: string, r: GateRow, n: number) {
  switch (id) {
    case "n":
      return <span className="mono num">{n}</span>;
    case "gate":
      return (
        <span className="gate-cell" title={r.question}>
          <span className="mono gate-id">{r.gate}</span>
          {r.subGate ? <span className="hint">{r.subGate.replaceAll("_", " ")}</span> : null}
        </span>
      );
    case "episode":
      return r.episodeLabel;
    case "verdict":
      return (
        <span className="verdict-cell">
          <VerdictPill verdict={r.verdict} notTriggered={r.notTriggered !== null} />
          {r.measured && r.uncalibrated ? <span className="uncal-tag" title="This gate is graded by a model that is not yet calibrated against human judgments">uncalibrated</span> : null}
        </span>
      );
    case "observed":
      return r.observed ?? <span className="hint">{r.notTriggered !== null ? `${r.notTriggered}` : NM}</span>;
    case "why":
      return r.measured ? r.why || <span className="hint">no reason recorded</span> : <span className="hint">{r.notTriggered !== null ? "not triggered" : NM}</span>;
    case "evidence":
      return <span className="mono num">{r.evidence.length}</span>;
    case "grader":
      return r.graderKind ?? <span className="hint">{NM}</span>;
    case "calibrated":
      return <span className="hint">not yet</span>;
    default:
      return r.time ? <span className="mono">{formatUtc(r.time)}</span> : <span className="hint">{NM}</span>;
  }
}

export interface GateTableProps {
  rows: readonly GateRow[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
  sorting: SortingState;
  onSortingChange: (s: SortingState) => void;
  visibility: ColumnVisibilityState;
  dense: boolean;
  /** Reports the displayed row order (after the table sorts) so prev/next follow what the person sees. */
  onOrder: (keys: string[]) => void;
}

/** The dense results table (TanStack Table). j/k and the arrows move between rows; Enter or a click opens the inspector. */
export function GateTable({ rows, selectedKey, onSelect, sorting, onSortingChange, visibility, dense, onOrder }: GateTableProps) {
  const table = useTable({
    features,
    columns,
    data: rows as GateRow[],
    state: { sorting, columnVisibility: visibility },
    onSortingChange: (u) => onSortingChange(typeof u === "function" ? u(sorting) : u),
    enableSortingRemoval: false,
    getRowId: (r: GateRow) => r.key,
  });
  const model = table.getRowModel().rows;
  const orderKey = model.map((r) => r.id).join("|");
  useEffect(() => onOrder(orderKey === "" ? [] : orderKey.split("|")), [orderKey, onOrder]);

  const move = (e: KeyboardEvent<HTMLTableRowElement>, i: number) => {
    const step = e.key === "j" || e.key === "ArrowDown" ? 1 : e.key === "k" || e.key === "ArrowUp" ? -1 : 0;
    if (step === 0 || e.metaKey || e.ctrlKey || e.altKey) return;
    e.preventDefault();
    const target = e.currentTarget.parentElement?.children[Math.min(model.length - 1, Math.max(0, i + step))] as HTMLElement | undefined;
    target?.focus();
  };

  return (
    <div className="table-scroll gate-scroll" role="region" aria-label="Gate results, scrollable" tabIndex={0}>
      <table className={`gate-table${dense ? " dense-rows" : ""}`}>
        <thead>
          {table.getHeaderGroups().map((g) => (
            <tr key={g.id}>
              {g.headers.map((h) => {
                const col = COLUMNS.find((c) => c.id === h.column.id);
                const dir = h.column.getIsSorted();
                return (
                  <th key={h.id} scope="col" aria-sort={dir === "asc" ? "ascending" : dir === "desc" ? "descending" : undefined} className={`col-${h.column.id}`}>
                    {h.column.getCanSort() ? (
                      <button type="button" className="sort-btn" onClick={() => h.column.toggleSorting(dir === "asc")}>
                        {col?.label}
                        <span aria-hidden="true">{dir === "asc" ? " ↑" : dir === "desc" ? " ↓" : ""}</span>
                      </button>
                    ) : (
                      col?.label
                    )}
                  </th>
                );
              })}
            </tr>
          ))}
        </thead>
        <tbody>
          {model.length === 0 ? (
            <tr>
              <td colSpan={COLUMNS.length} className="hint empty-row">No result matches these filters.</td>
            </tr>
          ) : null}
          {model.map((row, i) => (
            <tr
              key={row.id}
              tabIndex={0}
              data-gate={row.original.gate}
              data-episode={row.original.episodeId}
              data-measured={row.original.measured}
              aria-selected={row.id === selectedKey}
              className={row.id === selectedKey ? "selected" : undefined}
              onClick={() => onSelect(row.id)}
              onKeyDown={(e) => (e.key === "Enter" ? (e.preventDefault(), onSelect(row.id)) : move(e, i))}
            >
              {row.getVisibleCells().map((c) => (
                <td key={c.id} className={`col-${c.column.id}`}>
                  {cellOf(c.column.id, row.original, i + 1)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
