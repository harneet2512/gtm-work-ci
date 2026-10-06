import type { ReactNode } from "react";

/**
 * A table that scrolls inside its own frame at phone width instead of widening the page. The frame is a keyboard-focusable
 * labelled region (a scrollable area must be reachable without a pointer); the style is the shared `.table-scroll`.
 */
export function ScrollTable({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="table-scroll" role="region" aria-label={`${label}, scrollable`} tabIndex={0}>
      {children}
    </div>
  );
}
