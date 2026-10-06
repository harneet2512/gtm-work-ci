import Link from "next/link";
import type { ExplainerCard, ExplainerPage, ExplainerSection } from "@/lib/evals/explainer";
import { withDemo } from "@/lib/view/demo-link";

const ROWS: readonly { label: string; pick: (c: ExplainerCard) => string }[] = [
  { label: "What it checks", pick: (c) => c.what },
  { label: "Why we need it", pick: (c) => c.why },
  { label: "When it starts", pick: (c) => c.when },
  { label: "What happens with the result", pick: (c) => c.resultEffect },
];

/** One eval, written for a person: the question as the title, then what, why, when, the result and how it decides. */
function Card({ card }: { card: ExplainerCard }) {
  return (
    <article className="xcard" data-testid="explainer-card" data-soon={card.comingSoon ? "true" : undefined}>
      <p className="xcard-badges">
        <span className="xbadge">{card.cadence}</span>
        {card.comingSoon ? <span className="xbadge is-soon">Coming soon</span> : null}
      </p>
      <h3 className="xcard-q">{card.humanName}</h3>
      <dl className="xcard-rows">
        {ROWS.slice(0, 2).map((r) => (
          <div key={r.label} className="xcard-row">
            <dt>{r.label}</dt>
            <dd>{r.pick(card)}</dd>
          </div>
        ))}
        <div className="xcard-row">
          <dt>For example</dt>
          <dd className="xcard-example">{card.example}</dd>
        </div>
        {ROWS.slice(2).map((r) => (
          <div key={r.label} className="xcard-row">
            <dt>{r.label}</dt>
            <dd>{r.pick(card)}</dd>
          </div>
        ))}
        <div className="xcard-row">
          <dt>How it decides</dt>
          <dd>
            <strong>{card.decidesBy.label}.</strong> {card.decidesBy.meaning}
          </dd>
        </div>
      </dl>
    </article>
  );
}

function Section({ section, index }: { section: ExplainerSection; index: number }) {
  return (
    <section className="xsection" id={section.id} aria-labelledby={`${section.id}-title`} data-testid="explainer-section">
      <header className="xsection-head">
        <p className="xsection-kicker">
          <span className="xsection-n" aria-hidden="true">{index + 1}</span>
          {section.kicker}
        </p>
        <h2 className="xsection-title" id={`${section.id}-title`}>{section.title}</h2>
        <p className="xsection-q">{section.question}</p>
        <p className="xsection-intro">{section.intro}</p>
      </header>
      <div className="xcards">
        {section.cards.map((c) => (
          <Card key={c.key} card={c} />
        ))}
      </div>
    </section>
  );
}

/** The Explaining evals page: a plain-English intro, then every eval in the order of the product loop. Read only; no data beyond the contract. */
export function ExplainerView({ page, demo = false }: { page: ExplainerPage; demo?: boolean }) {
  const { intro, sections } = page;
  return (
    <div className="page xpage">
      <header className="xhead">
        <p className="eyebrow">gtm_ai · How it checks its work</p>
        <h1 className="xtitle">{intro.title}</h1>
        <p className="xlead">{intro.lead}</p>
        <div className="xintro-grid">
          <div>
            <h2 className="xh2">{intro.why_heading}</h2>
            <p className="xbody">{intro.why}</p>
          </div>
        </div>
        <h2 className="xh2">{intro.results_heading}</h2>
        <ul className="xresults">
          {intro.results.map((r) => (
            <li key={r.label}>
              <strong>{r.label}</strong>
              <span>{r.meaning}</span>
            </li>
          ))}
        </ul>
        <p className="xnote">{intro.honesty_note}</p>
        <nav className="xtoc" aria-label="The loop, in order">
          <ol>
            {sections.map((s) => (
              <li key={s.id}>
                <a href={`#${s.id}`}>
                  <span className="xtoc-kicker">{s.kicker}</span>
                  <span>{s.title}</span>
                </a>
              </li>
            ))}
          </ol>
        </nav>
      </header>
      {sections.map((s, i) => (
        <Section key={s.id} section={s} index={i} />
      ))}
      <p className="xfoot">
        Want to see these checks on a real decision? <Link href={withDemo("/evals/loop", demo)}>Open the loop</Link>.
      </p>
    </div>
  );
}
