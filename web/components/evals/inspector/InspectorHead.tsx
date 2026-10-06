import type { ReactNode } from "react";
import { InspectorNav, type InspectorTab } from "./InspectorNav";

/** The shared top of an inspector page: the eyebrow, the question as the title, one calm line, and the sub-navigation. */
export function InspectorHead({ tab, demo, episodeId, eyebrow, title, lead }: { tab: InspectorTab; demo: boolean; episodeId: string | null; eyebrow: string; title: string; lead?: ReactNode }) {
  return (
    <header className="insp-head-page">
      <InspectorNav active={tab} demo={demo} episodeId={episodeId} />
      <p className="eyebrow">{eyebrow}</p>
      <h1>{title}</h1>
      {lead ? <p className="lead">{lead}</p> : null}
    </header>
  );
}
