"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { activeSection, NAV_GROUPS } from "@/lib/view/nav";

/**
 * The left rail (HAR-145): the operator surface on top, then the product-area groups. The current
 * route's item is marked for sight and screen readers; groups are real headers, not separators.
 */
export function RailNav() {
  const current = activeSection(usePathname() ?? "/");
  // Demo mode is a URL flag; the rail must not drop it when it navigates (other params are page-specific and do not carry over).
  const demo = useSearchParams()?.get("demo") === "1";
  return (
    <nav className="railnav" aria-label="Sections">
      {NAV_GROUPS.map((g, i) => (
        <div key={g.label ?? g.items[0]?.href ?? i} className="rail-group">
          {g.label ? <p className="rail-label" title={g.title}>{g.label}</p> : null}
          <ul>
            {g.items.flatMap((s) => (demo && s.hiddenInDemo ? (s.demoLink ? [{ ...s, ...s.demoLink, key: s.href, inDemoAlt: true }] : []) : [{ ...s, key: s.href, inDemoAlt: false }])).map((s) => (
              <li key={s.key}>
                <Link href={demo ? `${s.href}${s.href.includes("?") ? "&" : "?"}demo=1` : s.href} aria-current={s.key === current ? "page" : undefined}>
                  {s.label}
                </Link>
              </li>
            ))}
          </ul>
          {i < NAV_GROUPS.length - 1 ? <hr className="rail-sep" /> : null}
        </div>
      ))}
    </nav>
  );
}
