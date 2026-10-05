import Link from "next/link";
import type { TraceStep } from "@/lib/evals/trace-strip";
import { formatDay } from "@/lib/format";

const STATUS_WORDS: Readonly<Record<TraceStep["status"], string>> = {
  done: "done",
  current: "this page",
  waiting: "not yet",
  blocked: "blocked",
  none: "not recorded",
};

function StepBody({ s }: { s: TraceStep }) {
  return (
    <>
      <span className="trace-dot" aria-hidden="true" />
      <span className="trace-label">{s.label}</span>
      <span className="trace-detail">{s.detail}</span>
      {s.at ? <time className="trace-at" dateTime={s.at}>{formatDay(s.at)}</time> : null}
      <span className="sr-only">, {STATUS_WORDS[s.status]}</span>
    </>
  );
}

/**
 * The decision's chain at a glance (HAR-97 trace viewer, condensed): Event N, the state change, the strategies,
 * these evals, the human's choice, the send and the inference. Each step links to the page that shows it; the
 * current one is marked, and steps that have not happened say so.
 */
export function TraceStrip({ steps }: { steps: readonly TraceStep[] }) {
  return (
    <nav className="trace-strip" aria-label="Decision trace">
      <ol>
        {steps.map((s) => (
          <li key={s.key} className={`trace-step is-${s.status}`}>
            {s.href ? (
              <Link href={s.href} className="trace-link" aria-current={s.status === "current" ? "step" : undefined}>
                <StepBody s={s} />
              </Link>
            ) : (
              <span className="trace-link">
                <StepBody s={s} />
              </span>
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}
