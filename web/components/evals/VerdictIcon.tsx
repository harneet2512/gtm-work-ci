import type { ReactNode } from "react";
import type { IconName } from "@/lib/evals/vocabulary";

// One icon set for the verdicts (eval wording table `icon`): the same shapes Slack shows as emoji. Stroke icons
// on a faint tinted disc, drawn in currentColor so the verdict tone sets the color and the word carries meaning.
const GLYPHS: Readonly<Record<IconName, ReactNode>> = {
  "x-circle": (
    <>
      <circle cx="12" cy="12" r="9" className="disc" />
      <path d="m9 9 6 6M15 9l-6 6" />
    </>
  ),
  "alert-triangle": (
    <>
      <path d="M10.3 4.2 2.7 17.4a2 2 0 0 0 1.7 3h15.2a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0Z" className="disc" />
      <path d="M12 9.5v4.2M12 17.2h.01" />
    </>
  ),
  "help-circle": (
    <>
      <circle cx="12" cy="12" r="9" className="disc" />
      <path d="M9.6 9.4a2.5 2.5 0 0 1 4.9.8c0 1.7-2.5 2.2-2.5 3.7M12 17.2h.01" />
    </>
  ),
  "check-circle": (
    <>
      <circle cx="12" cy="12" r="9" className="disc" />
      <path d="m8.2 12.4 2.6 2.6 5-5.2" />
    </>
  ),
  "minus-circle": (
    <>
      <circle cx="12" cy="12" r="9" className="disc" />
      <path d="M8.2 12h7.6" />
    </>
  ),
  "dashed-circle": <circle cx="12" cy="12" r="9" strokeDasharray="2.6 2.6" />,
};

/** Decorative: the verdict word next to it is what assistive technology reads. */
export function VerdictIcon({ name }: { name: IconName }) {
  return (
    <svg className="verdict-icon" viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      {GLYPHS[name]}
    </svg>
  );
}
