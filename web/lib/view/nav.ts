// The shell's information architecture (HAR-145): a compact left rail grouped by product area — the
// four control-plane groups — over the flat section list. activeSection is unchanged; the rail and the
// old top nav resolve the same route to the same current item. Area names are the HAR-145 ones with the Cliff message(s)
// they explain: one loop, and Cliff is its action surface, not a separate product area.
import { AREAS, areaShort, areaTitle } from "@/lib/evals/naming";

export interface Section {
  href: string;
  label: string;
  /** Operator-only: Demo mode hides it, so Play on Control stays the one visible trigger. */
  hiddenInDemo?: boolean;
  /** What Demo mode shows in place of a hidden operator page, when something presentable stands for it. */
  demoLink?: { href: string; label: string };
}

export interface NavGroup {
  /** Group label, or null for the ungrouped lead item (Control). */
  label: string | null;
  /** Full area title, shown as the tooltip of a shortened label. */
  title?: string;
  items: readonly Section[];
}

export const SECTIONS: readonly Section[] = [
  { href: "/control", label: "Control" },
  { href: "/", label: "Accounts" },
  { href: "/replay", label: "Replay", hiddenInDemo: true },
  { href: "/runs", label: "Runs" },
  { href: "/knowledge", label: "Knowledge" },
  { href: "/evals", label: "Evals" },
  { href: "/cliff", label: "Cliff messages" },
  { href: "/system", label: "System", hiddenInDemo: true, demoLink: { href: "/evals?view=proof", label: "System proof" } },
  { href: "/explaining-evals", label: "Explaining evals" },
];

/** The rail's groups carry the product-area names of the control plane; the lead item (Control) has none. */
export const NAV_GROUPS: readonly NavGroup[] = [
  { label: null, items: [SECTIONS[0]!] },
  { label: areaShort(AREAS[0]!), title: areaTitle(AREAS[0]!), items: [SECTIONS[1]!, SECTIONS[2]!] },
  { label: areaShort(AREAS[1]!), title: areaTitle(AREAS[1]!), items: [SECTIONS[3]!, SECTIONS[4]!, SECTIONS[5]!, SECTIONS[8]!] },
  { label: areaTitle(AREAS[2]!), items: [SECTIONS[6]!] },
  { label: areaTitle(AREAS[3]!), items: [SECTIONS[7]!] },
];

const within = (path: string, prefix: string): boolean => path === prefix || path.startsWith(`${prefix}/`);

/** The section href a pathname belongs to; "/" also owns /accounts/*. Null for a route of no section. */
export function activeSection(pathname: string): string | null {
  if (pathname === "/" || within(pathname, "/accounts")) return "/";
  return SECTIONS.find((s) => s.href !== "/" && within(pathname, s.href))?.href ?? null;
}
