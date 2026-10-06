"use client";

import { forwardRef } from "react";
import { BUCKETS, toggleIn, VIEWS, type GateFilters, type GateVerdict, type GraderKind } from "@/lib/evals/gate-table";
import { COLUMNS } from "./GateTable";

const VERDICTS: readonly GateVerdict[] = ["pass", "warn", "fail", "unknown"];
const GRADERS: readonly GraderKind[] = ["deterministic", "model", "hybrid"];

function Chips<T extends string>({ label, options, active, onToggle }: { label: string; options: readonly { id: T; label: string }[]; active: readonly T[]; onToggle: (id: T) => void }) {
  return (
    <div className="chip-group" role="group" aria-label={label}>
      <span className="chip-label">{label}</span>
      {options.map((o) => (
        <button key={o.id} type="button" className="chip" aria-pressed={active.includes(o.id)} onClick={() => onToggle(o.id)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

export interface ToolbarProps {
  filters: GateFilters;
  onFilters: (f: GateFilters) => void;
  viewId: string;
  onView: (id: string) => void;
  gates: readonly string[];
  episodes: readonly { id: string; label: string }[];
  hiddenColumns: readonly string[];
  onToggleColumn: (id: string) => void;
  dense: boolean;
  onDense: (dense: boolean) => void;
  shown: number;
  total: number;
}

/** Saved views, filter chips (bucket, gate, verdict, episode, grader, not measured), search (Ctrl/Cmd+K) and the Display menu. */
export const GateToolbar = forwardRef<HTMLInputElement, ToolbarProps>(function GateToolbar(p, searchRef) {
  const set = (patch: Partial<GateFilters>) => p.onFilters({ ...p.filters, ...patch });
  return (
    <div className="gate-toolbar">
      <div className="view-tabs" role="group" aria-label="Saved views">
        {VIEWS.map((v) => (
          <button key={v.id} type="button" className="view-tab" aria-pressed={p.viewId === v.id} onClick={() => p.onView(v.id)}>
            {v.label}
          </button>
        ))}
      </div>
      <div className="tool-row">
        <label className="gate-search">
          <span className="sr-only">Search gate results</span>
          <input ref={searchRef} type="search" placeholder="Search results" value={p.filters.query} onChange={(e) => set({ query: e.target.value })} />
          <kbd aria-hidden="true">Ctrl K</kbd>
        </label>
        <span className="hint num" role="status">{`${p.shown} of ${p.total} results`}</span>
        <details className="display-menu">
          <summary>Display</summary>
          <div className="menu-pop">
            <fieldset>
              <legend>Columns</legend>
              {COLUMNS.filter((c) => !c.locked).map((c) => (
                <label key={c.id}>
                  <input type="checkbox" checked={!p.hiddenColumns.includes(c.id)} onChange={() => p.onToggleColumn(c.id)} />
                  {c.label}
                </label>
              ))}
            </fieldset>
            <fieldset>
              <legend>Density</legend>
              <label>
                <input type="radio" name="density" checked={p.dense} onChange={() => p.onDense(true)} />
                Compact
              </label>
              <label>
                <input type="radio" name="density" checked={!p.dense} onChange={() => p.onDense(false)} />
                Comfortable
              </label>
            </fieldset>
          </div>
        </details>
      </div>
      <div className="chip-rows">
        <Chips label="Bucket" options={BUCKETS} active={p.filters.buckets} onToggle={(id) => set({ buckets: toggleIn(p.filters.buckets, id) })} />
        <label className="gate-select">
          <span className="chip-label">Gate</span>
          <select value={p.filters.gates[0] ?? ""} onChange={(e) => set({ gates: e.target.value ? [e.target.value] : [] })}>
            <option value="">All gates</option>
            {p.gates.map((g) => (
              <option key={g} value={g}>{g}</option>
            ))}
          </select>
        </label>
        <Chips label="Verdict" options={VERDICTS.map((v) => ({ id: v, label: v.toUpperCase() }))} active={p.filters.verdicts} onToggle={(id) => set({ verdicts: toggleIn(p.filters.verdicts, id) })} />
        <details className="more-filters">
          <summary>More filters</summary>
          <div className="more-body">
            <Chips label="Episode" options={p.episodes} active={p.filters.episodes} onToggle={(id) => set({ episodes: toggleIn(p.filters.episodes, id) })} />
            <Chips label="Grader" options={GRADERS.map((g) => ({ id: g, label: g }))} active={p.filters.graders} onToggle={(id) => set({ graders: toggleIn(p.filters.graders, id) })} />
            <div className="chip-group" role="group" aria-label="Measurement">
              <button type="button" className="chip" aria-pressed={p.filters.notMeasured} onClick={() => set({ notMeasured: !p.filters.notMeasured })}>
                Not measured
              </button>
              <button type="button" className="chip" aria-pressed={p.filters.notTriggered} onClick={() => set({ notTriggered: !p.filters.notTriggered })}>
                Not triggered
              </button>
            </div>
          </div>
        </details>
      </div>
    </div>
  );
});
