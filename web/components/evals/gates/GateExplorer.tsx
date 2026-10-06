"use client";

import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import type { SortingState, ColumnVisibilityState } from "@tanstack/react-table";
import { applyView, bucketSummaries, collapseNotMeasured, EMPTY_FILTERS, filterRows, episodesOf, viewById, type GateFilters, type GateRow } from "@/lib/evals/gate-table";
import type { GateDefinition } from "@/lib/evals/gate-cards";
import { GateInspector, type SpanStep } from "./GateInspector";
import { GateTable } from "./GateTable";
import { GateToolbar } from "./GateToolbar";
import { SummaryStrip } from "./SummaryStrip";

const MIN_W = 320;
const MAX_W = 760;
const STEP = 24;
const DEFAULT_SORT: SortingState = [{ id: "verdict", desc: false }];

export interface GateExplorerProps {
  rows: readonly GateRow[];
  /** The span path per episode id, in causal order (for the Trace tab). */
  paths: Readonly<Record<string, readonly SpanStep[]>>;
  /** The gates the catalog knows, for the gate chips. */
  gates: readonly string[];
  initialRow?: string | null;
  /** Execution mode per gate id (HAR-97), for the summary strip's mode line. */
  modes?: Readonly<Record<string, string>>;
  /** The gate definitions for the inspector (invariant and verdict criteria). */
  defs?: Readonly<Record<string, GateDefinition>>;
  /** `?gate=` from a card: the table opens narrowed to this gate. */
  initialGate?: string | null;
}

const isTyping = (t: EventTarget | null) => t instanceof HTMLElement && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable);

/** Evals table + inspector (Braintrust Logs pattern): summary strip, toolbar, results table, and a resizable right pane. */
export function GateExplorer({ rows, paths, gates, initialRow = null, defs = {}, initialGate = null, modes = {} }: GateExplorerProps) {
  const [filters, setFilters] = useState<GateFilters>(initialGate ? { ...EMPTY_FILTERS, gates: [initialGate] } : EMPTY_FILTERS);
  const [viewId, setViewId] = useState("default");
  const [sorting, setSorting] = useState<SortingState>(DEFAULT_SORT);
  const [hidden, setHidden] = useState<readonly string[]>(["grader", "time", "calibrated"]);
  const [dense, setDense] = useState(true);
  const [selected, setSelected] = useState<string | null>(initialRow);
  const [width, setWidth] = useState(440);
  const search = useRef<HTMLInputElement>(null);

  const [showNM, setShowNM] = useState(false);
  const matched = useMemo(() => filterRows(applyView(rows, viewId), filters), [rows, viewId, filters]);
  // Default view: measured results lead; not-measured gates fold into one expandable line (their counts stay in the strip).
  const folding = viewId === "default" && !filters.notMeasured;
  const { rows: visible, hidden: folded } = useMemo(() => (folding ? collapseNotMeasured(matched, showNM) : { rows: matched, hidden: 0 }), [folding, matched, showNM]);
  const foldable = folding && (folded > 0 || showNM) ? matched.filter((r) => !r.measured).length : 0;
  const [order, setOrder] = useState<string[]>([]);
  const summaries = useMemo(() => bucketSummaries(rows, new Map()), [rows]);
  const visibility: ColumnVisibilityState = useMemo(() => Object.fromEntries(hidden.map((id) => [id, false])), [hidden]);
  const current = visible.find((r) => r.key === selected) ?? null;
  const index = current ? order.indexOf(current.key) : -1;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        search.current?.focus();
      } else if (e.key === "Escape" && !isTyping(e.target)) setSelected(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const pickView = (id: string) => {
    setViewId(id);
    setFilters(viewById(id).filters);
    setSorting(DEFAULT_SORT);
  };
  const step = (d: 1 | -1) => setSelected(order[index + d] ?? selected);
  const resize = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key === "ArrowLeft") setWidth((w) => Math.min(MAX_W, w + STEP));
    if (e.key === "ArrowRight") setWidth((w) => Math.max(MIN_W, w - STEP));
  };
  const drag = (e: React.PointerEvent<HTMLDivElement>) => {
    const startX = e.clientX;
    const start = width;
    const move = (m: PointerEvent) => setWidth(Math.min(MAX_W, Math.max(MIN_W, start + startX - m.clientX)));
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  return (
    <div className="gate-explorer">
      <SummaryStrip summaries={summaries} modes={modes} />
      <GateToolbar
        ref={search}
        filters={filters}
        onFilters={setFilters}
        viewId={viewId}
        onView={pickView}
        gates={gates}
        episodes={episodesOf(rows)}
        hiddenColumns={hidden}
        onToggleColumn={(id) => setHidden((h) => (h.includes(id) ? h.filter((x) => x !== id) : [...h, id]))}
        dense={dense}
        onDense={setDense}
        shown={visible.length}
        total={rows.length}
      />
      <div className="gate-split" style={current ? ({ "--insp-w": `${width}px` } as React.CSSProperties) : undefined} data-open={current ? "true" : "false"}>
        <div className="gate-main">
        <GateTable rows={visible} onOrder={setOrder} selectedKey={current?.key ?? null} onSelect={setSelected} sorting={sorting} onSortingChange={setSorting} visibility={visibility} dense={dense} />
        {foldable > 0 ? (
          <button type="button" className="nm-toggle" aria-expanded={showNM} onClick={() => setShowNM(!showNM)}>
            {showNM ? `Hide ${foldable} gates with no result` : `${foldable} gates with no result yet (not measured or not triggered). Show them`}
          </button>
        ) : null}
        </div>
        {current ? (
          <>
            <div className="insp-resize" role="separator" aria-orientation="vertical" aria-label="Resize inspector" aria-valuemin={MIN_W} aria-valuemax={MAX_W} aria-valuenow={width} tabIndex={0} onKeyDown={resize} onPointerDown={drag} />
            <GateInspector key={current.key} row={current} def={defs[current.gate] ?? null} path={paths[current.episodeId] ?? []} onClose={() => setSelected(null)} onStep={step} canPrev={index > 0} canNext={index < order.length - 1} />
          </>
        ) : null}
      </div>
    </div>
  );
}
