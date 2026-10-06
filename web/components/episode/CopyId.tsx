"use client";

import { useState } from "react";

/** The full id in small mono text with a copy button; copying fails quietly (the id stays selectable). */
export function CopyId({ id }: { id: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(id);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };
  return (
    <span className="copy-id">
      <span className="mono hint" aria-label="Episode id">{id}</span>
      <button type="button" onClick={copy} aria-label="Copy episode id">{copied ? "Copied" : "Copy"}</button>
    </span>
  );
}
