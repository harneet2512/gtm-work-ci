import Link from "next/link";
import { isTestData } from "@/lib/evals/inspector/test-data";
import { withDemo } from "@/lib/view/demo-link";

export type InspectorTab = "loop" | "episode" | "decision" | "offline" | "health";

const TABS: readonly { id: InspectorTab; label: string }[] = [
  { id: "loop", label: "The loop" },
  { id: "episode", label: "Live episode" },
  { id: "decision", label: "The three options" },
  { id: "offline", label: "Offline checks" },
  { id: "health", label: "System health" },
];

/**
 * The inspector's own sub-navigation. `episodeId` carries the episode the person is looking at into the two episode views.
 * The tab for the page you are on is plain text; the old gate-results tables are an operator link (Demo mode hides it).
 */
export function InspectorNav({ active, demo, episodeId }: { active: InspectorTab; demo: boolean; episodeId: string | null }) {
  const href = (id: InspectorTab): string => {
    if (id === "loop") return "/evals/loop";
    if (id === "offline") return "/evals/offline";
    if (id === "health") return "/evals/health";
    if (!episodeId) return "/evals/episode";
    return id === "episode" ? `/evals/episode/${episodeId}` : `/evals/episode/${episodeId}/decision`;
  };
  return (
    <nav className="insp-nav" aria-label="Eval inspector">
      {isTestData() ? <span className="test-data-badge" data-testid="test-data-badge">TEST DATA</span> : null}
      {TABS.map((t) =>
        t.id === active ? (
          <span key={t.id} aria-current="page">
            {t.label}
          </span>
        ) : (
          <Link key={t.id} href={withDemo(href(t.id), demo)}>
            {t.label}
          </Link>
        ),
      )}
      {demo ? null : (
        <Link className="insp-nav-operator" href="/evals?view=gates">
          All gate results
        </Link>
      )}
    </nav>
  );
}
