/** The first value of a search param; Next may hand a repeated key as an array. */
export const firstParam = (v: string | string[] | undefined): string | undefined => (Array.isArray(v) ? v[0] : v);
