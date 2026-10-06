"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useEffect } from "react";

/**
 * Demo mode (HAR-145): a shareable `?demo=1` flag — pages may enlarge trajectories or pin context, but
 * it never changes the data. The toggle also marks <html data-demo> so CSS can react without a fetch. The flag is
 * remembered in localStorage so links that do not carry it (in-page links, a typed URL) do not silently drop demo mode:
 * a page opened without ?demo while the flag is stored gets it added back.
 */
const STORAGE_KEY = "ghost.demo";

export function DemoToggle() {
  const router = useRouter();
  const pathname = usePathname() ?? "/";
  const params = useSearchParams();
  const on = params.get("demo") === "1";

  useEffect(() => {
    document.documentElement.dataset.demo = on ? "1" : "";
  }, [on]);

  const query = params.toString();
  useEffect(() => {
    if (on) {
      window.localStorage.setItem(STORAGE_KEY, "1");
      return;
    }
    if (window.localStorage.getItem(STORAGE_KEY) !== "1") return;
    const next = new URLSearchParams(query);
    next.set("demo", "1");
    router.replace(`${pathname}?${next.toString()}`, { scroll: false });
  }, [on, query, pathname, router]);

  const toggle = () => {
    const next = new URLSearchParams(params.toString());
    if (on) {
      next.delete("demo");
      window.localStorage.removeItem(STORAGE_KEY);
    } else {
      next.set("demo", "1");
    }
    const q = next.toString();
    router.replace(`${pathname}${q ? `?${q}` : ""}`, { scroll: false });
  };

  return (
    <button type="button" className={`demo-toggle${on ? " on" : ""}`} onClick={toggle} aria-pressed={on} title="Demo mode: presentation emphasis — same real data">
      Demo
    </button>
  );
}
