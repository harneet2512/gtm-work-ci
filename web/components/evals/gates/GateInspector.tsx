"use client";

import Link from "next/link";
import { useState } from "react";
import { CRITERION_ORDER, type GateDefinition } from "@/lib/evals/gate-cards";
import { spanHref } from "@/lib/evals/gate-evidence";
import type { GateRow } from "@/lib/evals/gate-table";
import { formatUtc } from "@/lib/format";
import { VerdictPill } from "./VerdictPill";

export interface SpanStep {
  id: string;
  seq: number;
  title: string;
  time: string | null;
}

type Tab = "result" | "evidence" | "trace" | "grader";
const TABS: readonly { id: Tab; label: string }[] = [
  { id: "result", label: "Result" },
  { id: "evidence", label: "Evidence" },
  { id: "trace", label: "Trace" },
  { id: "grader", label: "Grader" },
];
const NM = "not measured";

function Fact({ term, children }: { term: string; children: React.ReactNode }) {
  return (
    <>
      <dt>{term}</dt>
      <dd>{children}</dd>
    </>
  );
}

function Definition({ def }: { def: GateDefinition | null }) {
  if (!def) return null;
  const given = CRITERION_ORDER.filter((v) => def.criteria[v] !== null);
  if (!def.invariant && !def.trigger && given.length === 0 && !def.criteriaNote) return null;
  return (
    <section className="insp-defs" aria-label="Gate definition">
      {def.invariant ? <p><strong>Catches:</strong> {def.invariant}</p> : null}
      {def.trigger ? <p><strong>Runs:</strong> {def.trigger}</p> : null}
      {given.length > 0 ? (
        <ul>
          {given.map((v) => (
            <li key={v}>
              <VerdictPill verdict={v} />
              <span>{def.criteria[v]}</span>
            </li>
          ))}
        </ul>
      ) : null}
      {def.criteriaNote ? <p className="hint">{def.criteriaNote}</p> : null}
    </section>
  );
}

function ResultTab({ row, def }: { row: GateRow; def: GateDefinition | null }) {
  return (
    <>
    <dl className="insp-facts">
      <Fact term="Question">{row.question}</Fact>
      <Fact term="Observed">{row.observed ?? <span className="hint">{NM}</span>}</Fact>
      <Fact term="Verdict">
        <VerdictPill verdict={row.verdict} notTriggered={row.notTriggered !== null} />
      </Fact>
      <Fact term="Why">
        {row.measured ? (
          row.why || <span className="hint">no reason recorded</span>
        ) : row.notTriggered !== null ? (
          <span className="hint">{`Not triggered: ${row.notTriggered} in this episode. It is not a pass.`}</span>
        ) : (
          <span className="hint">No result has been recorded for this gate, so it is not measured. It is not a pass.</span>
        )}
      </Fact>
      <Fact term="What this protects">{row.improves}</Fact>
    </dl>
    <Definition def={def} />
    </>
  );
}

function EvidenceTab({ row }: { row: GateRow }) {
  if (row.evidence.length === 0) return <p className="hint">{row.measured ? "This result cites no evidence, so it cannot read as a pass." : NM}</p>;
  return (
    <ol className="insp-evidence">
      {row.evidence.map((e) => (
        <li key={e.ref}>
          <span className="mono ev-kind">{e.kind.replaceAll("_", " ")}</span>
          {e.summary ? <blockquote>{e.summary}</blockquote> : <p className="hint">The record text is not available here.</p>}
          {e.href ? (
            <Link href={e.href} className="ev-link">
              {e.spanId ? `Open ${e.label} in the trace` : `Open ${e.label}`}
            </Link>
          ) : (
            <span className="hint">{`${e.label}: not found in this episode's trace`}</span>
          )}
        </li>
      ))}
    </ol>
  );
}

function TraceTab({ row, path }: { row: GateRow; path: readonly SpanStep[] }) {
  const at = row.spanId ? path.findIndex((s) => s.id === row.spanId) : -1;
  if (at < 0) return <p className="hint">{row.measured ? "The judged span is not in this episode's trace." : NM}</p>;
  return (
    <ol className="insp-path" aria-label="Span path to the judged span">
      {path.slice(0, at + 1).map((s, i) => (
        <li key={s.id} aria-current={i === at ? "step" : undefined}>
          <Link href={spanHref(row.episodeId, s.id)}>{`${s.seq}. ${s.title}`}</Link>
          <span className="hint mono">{s.time ? formatUtc(s.time) : NM}</span>
        </li>
      ))}
    </ol>
  );
}

function GraderTab({ row }: { row: GateRow }) {
  if (!row.measured) return <p className="hint">{NM}</p>;
  return (
    <dl className="insp-facts">
      <Fact term="Kind">{row.graderKind}</Fact>
      <Fact term="Model">{row.graderModel ?? (row.graderKind === "deterministic" ? "none: checked from stored records" : NM)}</Fact>
      <Fact term="Prompt version">{row.promptVersion ?? (row.graderKind === "deterministic" ? "none" : NM)}</Fact>
      <Fact term="Calibration">not yet calibrated</Fact>
    </dl>
  );
}

export interface InspectorProps {
  row: GateRow;
  def?: GateDefinition | null;
  path: readonly SpanStep[];
  onClose: () => void;
  onStep: (delta: 1 | -1) => void;
  canPrev: boolean;
  canNext: boolean;
}

/** The right pane: opens on a row click without navigating away. Esc closes it (handled by the explorer). */
export function GateInspector({ row, def = null, path, onClose, onStep, canPrev, canNext }: InspectorProps) {
  const [tab, setTab] = useState<Tab>("result");
  return (
    <aside className="gate-inspector" aria-label={`Inspector for ${row.gate}`}>
      <header className="insp-head">
        <button type="button" onClick={() => onStep(-1)} disabled={!canPrev} aria-label="Previous result">↑</button>
        <button type="button" onClick={() => onStep(1)} disabled={!canNext} aria-label="Next result">↓</button>
        <h2>
          <span className="mono">{row.gate}</span>
          {def?.name ? <span>{def.name}</span> : null}
          <span className="hint">{row.episodeLabel}</span>
        </h2>
        <button type="button" onClick={onClose} aria-label="Close inspector">×</button>
      </header>
      <div className="insp-tabs" role="tablist" aria-label="Inspector tabs">
        {TABS.map((t) => (
          <button key={t.id} type="button" role="tab" id={`insp-tab-${t.id}`} aria-selected={tab === t.id} aria-controls="insp-panel" onClick={() => setTab(t.id)}>
            {t.id === "evidence" ? `Evidence (${row.evidence.length})` : t.label}
          </button>
        ))}
      </div>
      <div id="insp-panel" role="tabpanel" aria-labelledby={`insp-tab-${tab}`} className="insp-body">
        {tab === "result" ? <ResultTab row={row} def={def} /> : null}
        {tab === "evidence" ? <EvidenceTab row={row} /> : null}
        {tab === "trace" ? <TraceTab row={row} path={path} /> : null}
        {tab === "grader" ? <GraderTab row={row} /> : null}
      </div>
    </aside>
  );
}
