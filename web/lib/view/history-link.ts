/**
 * The label of the control plane's link to the account's history. The held-out event is number `nextPosition`, so
 * `nextPosition - 1` events came before it; without a usable position the label names no number.
 */
export function historyLinkLabel(nextPosition: number | null | undefined): string {
  if (typeof nextPosition !== "number" || !Number.isInteger(nextPosition) || nextPosition < 2) return "View history before the next event";
  const count = nextPosition - 1;
  return `View history (${count} ${count === 1 ? "event" : "events"} before Event ${nextPosition})`;
}
