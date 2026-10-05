// The shell's information architecture (HAR-145): a compact left rail grouped by product area — the
// four control-plane groups — over the flat section list. activeSection is unchanged; the rail and the
// old top nav resolve the same route to the same current item.

export interface Section {
  href: string;
  label: string;
}

export interface NavGroup {
  /** Group label, or null for the ungrouped lead item (Control). */
  label: string | null;
  items: readonly Section[];
}

export const SECTIONS: readonly Section[] = [
  { href: "/control", label: "Control" },
  { href: "/", label: "Accounts" },
  { href: "/replay", label: "Replay" },
  { href: "/runs", label: "Runs" },
  { href: "/knowledge", label: "Knowledge" },
  { href: "/evals", label: "Evals" },
  { href: "/system", label: "System" },
];

export const NAV_GROUPS: readonly NavGroup[] = [
  { label: null, items: [SECTIONS[0]!] },
  { label: "Intelligence", items: [SECTIONS[1]!, SECTIONS[2]!] },
  { label: "Decision & Learning", items: [SECTIONS[3]!, SECTIONS[4]!, SECTIONS[5]!] },
  { label: null, items: [SECTIONS[6]!] },
];

const within = (path: string, prefix: string): boolean => path === prefix || path.startsWith(`${prefix}/`);

/** The section href a pathname belongs to; "/" also owns /accounts/*. Null for a route of no section. */
export function activeSection(pathname: string): string | null {
  if (pathname === "/" || within(pathname, "/accounts")) return "/";
  return SECTIONS.find((s) => s.href !== "/" && within(pathname, s.href))?.href ?? null;
}
