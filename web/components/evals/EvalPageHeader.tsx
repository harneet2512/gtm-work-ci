import Link from "next/link";
import type { ChoiceSummary, OptionRef } from "@/lib/evals/choice";
import { WORDING } from "@/lib/evals/vocabulary";
import { formatDay } from "@/lib/format";
import { withDemo } from "@/lib/view/demo-link";

export interface EvalPageHeaderProps {
  runId: string;
  account: string | null;
  accountId: string | null;
  decidedOn: string;
  /** What set the decision off, in plain words ("Email from Fatoumata Touré"). */
  trigger: string | null;
  choice: ChoiceSummary;
  /** Demo mode is on: carry ?demo=1 through every link. */
  demo?: boolean;
}

function Option({ option, kind, label }: { option: OptionRef; kind: "ghost" | "human"; label: string }) {
  return (
    <span className={`choice-option is-${kind}`}>
      <span className="choice-label">{label}</span>
      <span className="option-letter" aria-hidden="true">
        {option.letter}
      </span>
      <span className="choice-title">
        <span className="sr-only">Option {option.letter}: </span>
        {option.title}
      </span>
    </span>
  );
}

function ChoiceRow({ choice }: { choice: ChoiceSummary }) {
  const { ghostPick, chosen, actor, agreed } = choice;
  return (
    <p className="choice-row">
      {ghostPick ? <Option option={ghostPick} kind="ghost" label={WORDING.phrases.ghost_pick} /> : <span className="choice-none">Ghost recommends none of these</span>}
      {chosen && actor ? (
        agreed ? (
          <span className="choice-agreed">{actor} chose Ghost's pick</span>
        ) : (
          <>
            <span className="choice-arrow" aria-hidden="true">
              →
            </span>
            <Option option={chosen} kind="human" label={`Chosen by ${actor}`} />
          </>
        )
      ) : (
        <span className="choice-none">Awaiting the human's choice</span>
      )}
    </p>
  );
}

/** The eval page's head: where you are, which account and decision, when, and who chose what. */
export function EvalPageHeader({ runId, account, accountId, decidedOn, trigger, choice, demo = false }: EvalPageHeaderProps) {
  return (
    <header className="eval-head">
      <div className="eval-head-top">
        <ol className="crumbs" aria-label="Breadcrumb">
          <li>
            <Link href={withDemo("/runs", demo)}>Runs</Link>
          </li>
          {account && accountId ? (
            <li>
              <Link href={withDemo(`/accounts/${accountId}`, demo)}>{account}</Link>
            </li>
          ) : null}
          <li>
            <Link href={withDemo(`/runs/${runId}`, demo)}>Decision</Link>
          </li>
          <li aria-current="page">Evals</li>
        </ol>
        <nav className="head-actions" aria-label="Related pages">
          <Link className="btn" href={withDemo(`/runs/${runId}`, demo)}>
            Decision chain
          </Link>
          <Link className="btn" href={withDemo("/evals", demo)}>
            All evals
          </Link>
        </nav>
      </div>
      <h1>{account ? `${account}: how Ghost judged this decision` : "How Ghost judged this decision"}</h1>
      <p className="eval-subtitle">
        Decision of <time dateTime={decidedOn}>{formatDay(decidedOn)}</time>
        {trigger ? ` · after: ${trigger}` : ""}. Each verdict comes with its reason and the evidence behind it; none is collapsed into a score.
      </p>
      <ChoiceRow choice={choice} />
    </header>
  );
}
