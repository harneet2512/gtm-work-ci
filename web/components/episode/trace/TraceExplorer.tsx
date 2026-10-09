"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { VerdictPill } from "@/components/evals/gates/VerdictPill";
import type { GateResult, TraceSpan } from "@/lib/api/types";
import { formatUtc } from "@/lib/format";
import type { ThreadMessage } from "@/lib/view/trace-thread";
import { aggregateVerdicts, buildTraceTree, gapText, pickSpan, type TreeSpan } from "@/lib/view/trace-tree";

type Tab = "io" | "evaluators" | "thread";
const STATUS_WORD: Readonly<Record<TraceSpan["status"], string>> = { recorded: "recorded", pending: "pending", not_recorded: "not recorded" };
const MARK: Readonly<Record<TraceSpan["status"], string>> = { recorded: "●", pending: "○", not_recorded: "—" };

function SpanButton({ t, selected, onPick }: { t: TreeSpan; selected: boolean; onPick: () => void }) {
  const s = t.span;
  const agg = aggregateVerdicts(t.verdicts);
  return (
    <li>
      <button type="button" className={`span-btn st-${s.status}`} id={`span-${s.id}`} aria-current={selected ? "true" : undefined} onClick={onPick}>
        <span aria-hidden="true">{MARK[s.status]}</span>
        <span className="span-title">{s.title}</span>
        <span className="sr-only">{STATUS_WORD[s.status]}</span>
        {gapText(t.gapMs) ? <span className="span-gap hint num" title="Time since the previous span on the same clock">{gapText(t.gapMs)}</span> : null}
        {agg ? (
          <span className="span-agg">
            <VerdictPill verdict={agg.worst} />
            <span className="hint num">{agg.text}</span>
          </span>
        ) : null}
      </button>
    </li>
  );
}

function IoTab({ span }: { span: TraceSpan }) {
  const [raw, setRaw] = useState(false);
  const attrs = Object.entries(span.attributes ?? {});
  return (
    <div>
      <button type="button" className="chip" aria-pressed={raw} onClick={() => setRaw(!raw)}>Raw JSON</button>
      {raw ? (
        <pre className="raw-json">{JSON.stringify(span, null, 2)}</pre>
      ) : (
        <div className="io-grid">
          <section aria-label="Input">
            <h3>Input</h3>
            {span.refs.length === 0 ? <p className="hint">no rows behind this span</p> : null}
            <ul className="io-refs">
              {span.refs.map((r) => (
                <li key={`${r.kind}${r.id}`}>
                  <span className="hint">{r.kind.replaceAll("_", " ")}</span> <span className="mono">{r.id}</span>
                </li>
              ))}
            </ul>
          </section>
          <section aria-label="Output">
            <h3>Output</h3>
            <p>{span.summary}</p>
            <p className="hint">{span.occurred_at ? formatUtc(span.occurred_at) : "time not recorded"}</p>
            <dl className="insp-facts">
              {attrs.map(([k, v]) => (
                <div key={k} className="attr-row">
                  <dt>{k.replaceAll("_", " ")}</dt>
                  <dd className="mono">{typeof v === "object" ? JSON.stringify(v) : String(v)}</dd>
                </div>
              ))}
            </dl>
          </section>
        </div>
      )}
    </div>
  );
}

function EvaluatorsTab({ gates, episodeId }: { gates: readonly GateResult[]; episodeId: string }) {
  if (gates.length === 0) return <p className="hint">No gate result is attached to this span, so it is not measured.</p>;
  return (
    <ul className="eval-list">
      {gates.map((g) => (
        <li key={g.id}>
          <span className="mono gate-id">{g.gate}</span>
          <VerdictPill verdict={g.verdict === "pass" && g.evidence_refs.length === 0 ? "unknown" : g.verdict} />
          <p>{g.why}</p>
          <Link href={`/evals?episode=${episodeId}`}>Open in the results table</Link>
        </li>
      ))}
    </ul>
  );
}

function ThreadTab({ span, thread }: { span: TraceSpan; thread: readonly ThreadMessage[] }) {
  if (span.kind !== "cliff_message") return <p className="hint">The thread belongs to the Cliff steps. Select a Cliff message span.</p>;
  if (thread.length === 0) return <p className="hint">No Cliff message content is recorded for this episode.</p>;
  return (
    <ol className="thread">
      {thread.map((m) => (
        <li key={m.id} className="thread-msg">
          <p className="thread-who">{`Message ${m.id.slice(1)} · gtm_ai · ${m.title}`}</p>
          {m.lines.map((l) => (
            <p key={l}>{l}</p>
          ))}
        </li>
      ))}
    </ol>
  );
}

/** The Braintrust-style trace view: span tree on the left, the selected span's detail on the right. */
export function TraceExplorer({ spans, gates, thread, episodeId, initialSpan }: { spans: readonly TraceSpan[]; gates: readonly GateResult[]; thread: readonly ThreadMessage[]; episodeId: string; initialSpan: string | null }) {
  const phases = useMemo(() => buildTraceTree(spans, gates), [spans, gates]);
  const [spanId, setSpanId] = useState(() => pickSpan(spans, initialSpan)?.id ?? null);
  const [tab, setTab] = useState<Tab>("io");
  const current = spans.find((s) => s.id === spanId) ?? null;
  const mine = current ? gates.filter((g) => g.span_id === current.id) : [];

  const pick = (id: string) => {
    setSpanId(id);
    const url = new URL(window.location.href);
    url.searchParams.set("span", id);
    window.history.replaceState(null, "", url);
  };

  if (spans.length === 0) return <p className="hint">No trace is recorded for this episode.</p>;
  return (
    <div className="trace-split">
      <nav aria-label="Span tree" className="span-tree">
        {phases.map((p) => (
          <section key={p.id} aria-label={p.label}>
            <h3 className="phase-h">{p.label}</h3>
            <ul>
              {p.spans.map((t) => (
                <SpanButton key={t.span.id} t={t} selected={t.span.id === spanId} onPick={() => pick(t.span.id)} />
              ))}
            </ul>
          </section>
        ))}
      </nav>
      {current ? (
        <section className="span-detail" aria-label={`Span ${current.title}`} data-span={current.id}>
          <header>
            <h2>{current.title}</h2>
          </header>
          <div className="insp-tabs" role="tablist" aria-label="Span detail">
            <button type="button" role="tab" aria-selected={tab === "io"} onClick={() => setTab("io")}>Input / Output</button>
            <button type="button" role="tab" aria-selected={tab === "evaluators"} onClick={() => setTab("evaluators")}>{`Evaluators (${mine.length})`}</button>
            <button type="button" role="tab" aria-selected={tab === "thread"} onClick={() => setTab("thread")}>Thread</button>
          </div>
          <div role="tabpanel" className="insp-body">
            {tab === "io" ? <IoTab key={current.id} span={current} /> : null}
            {tab === "evaluators" ? <EvaluatorsTab gates={mine} episodeId={episodeId} /> : null}
            {tab === "thread" ? <ThreadTab span={current} thread={thread} /> : null}
          </div>
        </section>
      ) : null}
    </div>
  );
}
