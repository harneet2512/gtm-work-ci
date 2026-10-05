"use client";

import { useEffect } from "react";

/**
 * Landing for a deep link such as /episodes/:id?node=cliff (the Slack "View trace" target): scrolls the selected
 * trajectory node into view and moves keyboard focus to its link, so the inspector's subject is also where the
 * reader's eyes and focus are. A node id that is not on the trajectory does nothing.
 */
export function FocusNode({ nodeId }: { nodeId: string | null }) {
  useEffect(() => {
    if (!nodeId) return;
    const link = document.getElementById(`node-${nodeId}`)?.querySelector("a");
    if (!link) return;
    link.scrollIntoView({ block: "center" });
    link.focus({ preventScroll: true });
  }, [nodeId]);
  return null;
}
