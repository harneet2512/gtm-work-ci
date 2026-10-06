"use client";

import { useEffect, useState, type ReactNode } from "react";

const KEY = "ghost.inspector.collapsed";

/**
 * The right inspector (HAR-145): a collapsible, resizable drawer the current page fills through the
 * @inspector slot. Empty slot → no frame at all. Collapse persists across routes; the drawer keeps the
 * main context visible rather than navigating away.
 */
export function InspectorFrame({ children }: { children: ReactNode }) {
  const [collapsed, setCollapsed] = useState(false);
  const [hydrated, setHydrated] = useState(false);

  useEffect(() => {
    setCollapsed(window.localStorage.getItem(KEY) === "1");
    setHydrated(true);
  }, []);

  const toggle = () => {
    setCollapsed((c) => {
      window.localStorage.setItem(KEY, c ? "0" : "1");
      return !c;
    });
  };

  if (children == null) return null;

  return (
    <aside className={`inspector${collapsed ? " collapsed" : ""}${hydrated ? "" : " unhydrated"}`} aria-label="Inspector">
      <button
        type="button"
        className="inspector-toggle"
        onClick={toggle}
        aria-expanded={!collapsed}
        aria-label={collapsed ? "Expand inspector" : "Collapse inspector"}
        title={collapsed ? "Expand inspector" : "Collapse inspector"}
      >
        {collapsed ? "‹" : "›"}
      </button>
      <div className="inspector-body" hidden={collapsed}>
        {children}
      </div>
    </aside>
  );
}
