import Link from "next/link";
import { accountViewHref } from "@/lib/view/account-href";
import { withDemo } from "@/lib/view/demo-link";
import { historyLinkLabel } from "@/lib/view/history-link";

interface Props {
  accountId: string;
  /** The held-out event's position; null when nothing is held out. */
  nextPosition: number | null;
  demo: boolean;
}

/** A plain link to the account's Before Play view: it shows the saved history and never runs or releases anything. */
export function HistoryLink({ accountId, nextPosition, demo }: Props) {
  return (
    <p className="history-link">
      <Link href={withDemo(accountViewHref(accountId, {}, "before"), demo)}>{historyLinkLabel(nextPosition)}</Link>
    </p>
  );
}
