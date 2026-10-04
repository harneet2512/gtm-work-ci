import type { SelectedView } from "@/lib/evals/selected";
import { verdictWording } from "@/lib/evals/vocabulary";
import { VerdictIcon } from "./VerdictIcon";

/**
 * The page's first line: the judgment on the action in view, not the machinery (spec: "the rep should understand
 * in 3 seconds what is risky about this action and why"). The worst verdict as a title, its reason, and which
 * option this is.
 */
export function VerdictBanner({ view, letter }: { view: SelectedView; letter: string }) {
  const h = view.headline;
  const tone = h.verdict ?? "not_checked";
  const who = view.isChosen && view.chosenBy ? ` · chosen by ${view.chosenBy}` : view.isGhostPick ? " · Ghost's pick" : "";
  const context = `Option ${letter} · ${view.title}${who}`;
  const more = h.more > 0 ? ` ${h.more === 1 ? "1 more verdict" : `${h.more} more verdicts`} to look at below.` : "";
  return (
    <section className={`verdict-banner v-${tone}`} aria-labelledby="verdict-banner-h">
      <span className="banner-icon">
        <VerdictIcon name={verdictWording(tone).icon} />
      </span>
      <div>
        <h2 id="verdict-banner-h">{h.title}</h2>
        {h.detail ? <p className="banner-detail">{h.detail}</p> : null}
        <p className="banner-context">
          {context}.{more}
        </p>
      </div>
    </section>
  );
}
