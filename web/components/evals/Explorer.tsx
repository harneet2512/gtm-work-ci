"use client";

import { useMemo, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import type { EvalRow, ExplorerQuery, SortKey } from "@/lib/view/evals-explorer";
import { DEFAULT_QUERY, filterRows, sortRows } from "@/lib/view/evals-explorer";
import { ScrollTable } from "@/components/ScrollTable";

const VERDICTS = ["fail", "warn", "abstain", "pass", "not_relevant"] as const;
const KINDS = ["deterministic", "semantic", "trace", "human_delta"] as const;
const MARK: Record<string, string> = { fail: "✗", warn: "!", abstain: "◌", pass: "✓", not_relevant: "·", not_checked: "?" };

const short = (id: string) => (id.length > 13 ? `${id.slice(0, 13)}…` : id);

/**
 * The results explorer (HAR-145): one dense table of every produced eval result. Search, verdict / kind /
 * blocking filters and column sorting are client-local (the rows are already loaded); selecting a row
 * writes ?result=<id> so the inspector slot and the URL agree.
 */
export function Explorer({ rows, selected }: { rows: EvalRow[]; selected: string | null }) {
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const [q, setQ] = useState<ExplorerQuery>({ ...DEFAULT_QUERY });

  const shown = useMemo(() => sortRows(filterRows(rows, q), q), [rows, q]);

  const select = (id: string) => {
    const next = new URLSearchParams(params.toString());
    next.set("view", "results");
    next.set("result", id);
    router.replace(`${pathname}?${next.toString()}`, { scroll: false });
  };

  const setSort = (key: SortKey) =>
    setQ((s) => ({ ...s, sort: key, dir: s.sort === key && s.dir === "asc" ? "desc" : "asc" }));

  const th = (key: SortKey, label: string) => (
    <th aria-sort={q.sort === key ? (q.dir === "asc" ? "ascending" : "descending") : undefined}>
      <button type="button" className="thsort" onClick={() => setSort(key)}>
        {label}
        {q.sort === key ? (q.dir === "asc" ? " ↑" : " ↓") : ""}
      </button>
    </th>
  );

  return (
    <div className="explorer">
      <div className="toolbar">
        <input
          type="search"
          value={q.q}
          onChange={(e) => setQ((s) => ({ ...s, q: e.target.value }))}
          placeholder="Search evals, reasons, options…"
          aria-label="Search eval results"
        />
        <div className="chipset" role="group" aria-label="Verdict filter">
          {VERDICTS.map((v) => (
            <button
              key={v}
              type="button"
              className={`chip ${q.verdict === v ? "on" : ""}`}
              aria-pressed={q.verdict === v}
              onClick={() => setQ((s) => ({ ...s, verdict: s.verdict === v ? null : v }))}
            >
              {MARK[v]} {v.replace("_", " ")}
            </button>
          ))}
        </div>
        <div className="chipset" role="group" aria-label="Kind filter">
          {KINDS.map((k) => (
            <button
              key={k}
              type="button"
              className={`chip ${q.kind === k ? "on" : ""}`}
              aria-pressed={q.kind === k}
              onClick={() => setQ((s) => ({ ...s, kind: s.kind === k ? null : k }))}
            >
              {k.replace("_", " ")}
            </button>
          ))}
          <button
            type="button"
            className={`chip ${q.blocking ? "on" : ""}`}
            aria-pressed={q.blocking}
            onClick={() => setQ((s) => ({ ...s, blocking: !s.blocking }))}
          >
            Blocks send
          </button>
        </div>
        <span className="count">
          {shown.length} of {rows.length} results
        </span>
      </div>

      <ScrollTable label="Eval results"><table className="dense explorer-table">
        <thead>
          <tr>
            <th aria-label="Verdict" />
            {th("name", "Eval")}
            {th("run", "Run")}
            {th("candidate", "Option")}
            <th>Kind</th>
            <th>Class</th>
            <th>Reason</th>
            {th("created", "At")}
          </tr>
        </thead>
        <tbody>
          {shown.map((r) => (
            <tr
              key={r.resultId}
              className={`vrow st-${r.verdict}${r.resultId === selected ? " selected" : ""}`}
              onClick={() => select(r.resultId)}
              tabIndex={0}
              aria-selected={r.resultId === selected}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault(); // Space would otherwise scroll the page
                  select(r.resultId);
                }
              }}
            >
              <td className="mark mono" title={r.blocking ? `${r.verdict} — blocks send` : r.verdict}>
                <span aria-hidden="true">
                  {MARK[r.verdict]}
                  {r.blocking ? "⛔" : ""}
                </span>
                <span className="sr-only">{`${r.verdict.replace("_", " ")}${r.blocking ? ", blocks send" : ""}`}</span>
              </td>
              <td className="ename">
                {r.name}
                <span className="etype mono">{r.evalType}</span>
              </td>
              <td className="mono">{short(r.runId)}</td>
              <td>
                <span className="cand mono">{r.candidate}</span>
                {r.isGhostPick ? <span className="tag">★</span> : null}
                {r.isChosen ? <span className="tag">✓</span> : null}
                <span className="ctitle">{r.candidateTitle}</span>
              </td>
              <td>{r.kind ?? "—"}</td>
              <td>{r.evidenceClass?.replace("_", " ") ?? "—"}</td>
              <td className="reason">{r.reason ?? "—"}</td>
              <td className="mono">{r.createdAt?.slice(0, 10) ?? "—"}</td>
            </tr>
          ))}
          {shown.length === 0 ? (
            <tr>
              <td colSpan={8} className="empty">
                No eval results match — clear a filter or widen the search.
              </td>
            </tr>
          ) : null}
        </tbody>
      </table></ScrollTable>
    </div>
  );
}
