// The account page's own URL for a Play view: shared by its Before/After toggle and by links that open it from elsewhere.
export interface AccountHrefQuery {
  event?: string;
  cutoff?: string;
}

export function accountViewHref(id: string, query: AccountHrefQuery, view: "before" | "after"): string {
  const params = new URLSearchParams({ view });
  if (query.event) params.set("event", query.event);
  if (query.cutoff) params.set("cutoff", query.cutoff);
  return `/accounts/${id}?${params.toString()}`;
}
