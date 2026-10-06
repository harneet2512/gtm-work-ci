"use client";

import { forwardRef } from "react";
import { BUCKETS, toggleIn, VIEWS, type GateFilters, type GateMode, type GraderKind } from "@/lib/evals/gate-table";
import { MODES } from "@/lib/evals/gate-cards";
import { STATUS_ORDER, statusWord } from "@/lib/evals/naming";
import { COLUMNS } from "./GateTable";

const MODE_IDS: readonly GateMode[] = ["live_required", "live_conditional", "offline_benchmark", "continuous_aggregate"];
/** HAR-145's six failure impacts, in the order the registry lists them. */
export const IMPACTS: readonly string[] = ["blocks current action", "requires human review", "triggers recomputation", "prevents knowledge promotion", "marks capability unreliable", "monitoring only"];
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
        <Chips label="Status" options={STATUS_ORDER.map((s) => ({ id: s, label: statusWord(s) }))} active={p.filters.statuses} onToggle={(id) => set({ statuses: toggleIn(p.filters.statuses, id) })} />
        <Chips label="Mode" options={MODE_IDS.map((m) => ({ id: m, label: MODES[m].badge }))} active={p.filters.modes} onToggle={(id) => set({ modes: toggleIn(p.filters.modes, id) })} />
        <Chips label="Impact" options={IMPACTS.map((i) => ({ id: i, label: i }))} active={p.filters.impacts} onToggle={(id) => set({ impacts: toggleIn(p.filters.impacts, id) })} />
        <details className="more-filters">
          <summary>More filters</summary>
          <div className="more-body">
            <Chips label="Episode" options={p.episodes} active={p.filters.episodes} onToggle={(id) => set({ episodes: toggleIn(p.filters.episodes, id) })} />
            <Chips label="Grader" options={GRADERS.map((g) => ({ id: g, label: g }))} active={p.filters.graders} onToggle={(id) => set({ graders: toggleIn(p.filters.graders, id) })} />
          </div>
        </details>
      </div>
    </div>
  );
});
