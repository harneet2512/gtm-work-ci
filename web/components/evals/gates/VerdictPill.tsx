import type { GateVerdict } from "@/lib/evals/gate-table";
import { displayStatus, statusWord, type DisplayStatus } from "@/lib/evals/naming";
import type { IconName } from "@/lib/evals/vocabulary";
import { VerdictIcon } from "@/components/evals/VerdictIcon";

/** Icon and tone per display status: six, each visually distinct, and none of the last two looks like PASS. */
const LOOK: Readonly<Record<DisplayStatus, { icon: IconName; cls: string }>> = {
  pass: { icon: "check-circle", cls: "v-pass" },
  warn: { icon: "alert-triangle", cls: "v-warn" },
  fail: { icon: "x-circle", cls: "v-fail" },
  unknown: { icon: "help-circle", cls: "v-abstain" },
  not_run: { icon: "dashed-circle", cls: "v-not_checked v-not-run" },
  not_applicable: { icon: "minus-circle", cls: "v-not_checked v-not-applicable" },
};
/** The stable hook the tests and filters use; the words on screen are the HAR-145 six. */
const DATA: Readonly<Record<DisplayStatus, string>> = { pass: "pass", warn: "warn", fail: "fail", unknown: "unknown", not_run: "not_measured", not_applicable: "not_triggered" };

/** The status as icon + word + tone (never color alone). A null verdict is NOT RUN (or NOT APPLICABLE), which is not a pass. */
export function VerdictPill({ verdict, notTriggered = false }: { verdict: GateVerdict | null; notTriggered?: boolean }) {
  const status = displayStatus(verdict, notTriggered);
  const look = LOOK[status];
  return (
    <span className={`verdict gate-pill ${look.cls}`} data-verdict={DATA[status]} data-status={status}>
      <VerdictIcon name={look.icon} />
      <span className="verdict-word">{statusWord(status)}</span>
    </span>
  );
}
