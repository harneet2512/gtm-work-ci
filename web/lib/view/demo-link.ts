// Demo mode is a URL flag (?demo=1); an in-page link that drops it brings operator-only views back. withDemo carries the
// flag through a link, keeping any query and leaving the fragment last.
export function withDemo(href: string, demo: boolean): string {
  if (!demo) return href;
  const hash = href.indexOf("#");
  const path = hash === -1 ? href : href.slice(0, hash);
  const fragment = hash === -1 ? "" : href.slice(hash);
  const query = path.indexOf("?");
  const params = new URLSearchParams(query === -1 ? "" : path.slice(query + 1));
  if (params.get("demo") === "1") return href;
  const base = query === -1 ? path : path.slice(0, query);
  const rest = query === -1 ? "" : path.slice(query + 1);
  return `${base}?${rest ? `${rest}&` : ""}demo=1${fragment}`;
}
