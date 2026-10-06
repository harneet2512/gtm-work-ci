import type { SelectedView } from "@/lib/evals/selected";
import { verdictWording } from "@/lib/evals/vocabulary";
import type { VerdictCounts } from "@/lib/view/run-chain";
import { VerdictIcon } from "./VerdictIcon";
import { VerdictStrip } from "./VerdictStrip";

/**
 * The page's first line: the judgment on the action in view, not the machinery (spec: "the rep should understand
 * in 3 seconds what is risky about this action and why"). The worst verdict as a title, its reason, the words it
 * rests on and who said them, which option this is, and the option's verdicts side by side.
 */
export function VerdictBanner({ view, letter, counts }: { view: SelectedView; letter: string; counts?: VerdictCounts }) {
  const h = view.headline;
  const tone = h.verdict ?? "not_checked";
  const who = view.isChosen && view.chosenBy ? ` · chosen by ${view.chosenBy}` : view.isGhostPick ? " · gtm_ai's pick" : "";
  const context = `Option ${letter} · ${view.title}${who}`;
  const more = h.more > 0 ? ` ${h.more === 1 ? "1 more verdict" : `${h.more} more verdicts`} to look at below.` : "";
  const worst = view.lines[0];
  const quote = worst && worst.verdict !== "pass" ? worst.evidence.find((e) => e.quote) : undefined;
  return (
    <section className={`verdict-banner v-${tone}`} aria-labelledby="verdict-banner-h">
      <span className="banner-icon">
        <VerdictIcon name={verdictWording(tone).icon} />
      </span>
      <div className="banner-body">
        <h2 id="verdict-banner-h">{h.title}</h2>
        {h.detail ? <p className="banner-detail">{h.detail}</p> : null}
        {quote ? (
          <figure className="banner-quote">
            <blockquote>“{quote.quote}”</blockquote>
            <figcaption>
              {quote.who ?? "Speaker not recorded"}
              {quote.when ? ` · ${quote.when}` : ""}
              {quote.source ? ` · ${quote.source}` : ""}
            </figcaption>
          </figure>
        ) : null}
        <p className="banner-context">
          {context}.{more}
        </p>
      </div>
      {counts ? (
        <div className="banner-side">
          <VerdictStrip counts={counts} />
        </div>
      ) : null}
    </section>
  );
}
