/** "2026-09-29 15:42 UTC": stable across server and browser time zones (no hydration drift). */
export function formatUtc(iso: string): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return iso;
  return `${new Date(ms).toISOString().slice(0, 16).replace("T", " ")} UTC`;
}

export const shortId = (id: string): string => id.slice(0, 8);

/** A readable one-line rendering of an account-state field value (string, list of items, or other JSON). */
export function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    if (value.length === 0) return "none";
    return value
      .map((item) => (typeof item === "object" && item !== null && "text" in item ? String((item as { text: unknown }).text) : JSON.stringify(item)))
      .join("; ");
  }
  return JSON.stringify(value);
}
