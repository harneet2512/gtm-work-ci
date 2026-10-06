import type { GateVerdict } from "@/lib/evals/gate-table";
import type { IconName } from "@/lib/evals/vocabulary";
import { VerdictIcon } from "@/components/evals/VerdictIcon";

const WORD: Readonly<Record<GateVerdict | "none" | "skipped", { label: string; icon: IconName; cls: string }>> = {
  pass: { label: "PASS", icon: "check-circle", cls: "v-pass" },
  warn: { label: "WARN", icon: "alert-triangle", cls: "v-warn" },
  fail: { label: "FAIL", icon: "x-circle", cls: "v-fail" },
  unknown: { label: "UNKNOWN", icon: "help-circle", cls: "v-abstain" },
  none: { label: "NOT MEASURED", icon: "dashed-circle", cls: "v-not_checked" },
  skipped: { label: "NOT TRIGGERED", icon: "minus-circle", cls: "v-not_checked" },
};

/** The verdict as icon + word + tone (never color alone). A null verdict is "not measured", which is not a pass. */
export function VerdictPill({ verdict, notTriggered = false }: { verdict: GateVerdict | null; notTriggered?: boolean }) {
  const w = WORD[verdict ?? (notTriggered ? "skipped" : "none")];
  return (
    <span className={`verdict gate-pill ${w.cls}`} data-verdict={verdict ?? (notTriggered ? "not_triggered" : "not_measured")}>
      <VerdictIcon name={w.icon} />
      <span className="verdict-word">{w.label}</span>
    </span>
  );
}
