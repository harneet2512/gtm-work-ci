// The canvas palette, read from the app's theme tokens (app/styles/tokens.css) so the graph follows the
// light and dark themes. One gtm_ai accent (focus, selection, what gtm_ai knows); status colors only for
// what the event added, changed or removed.

export interface Palette {
  canvas: string;
  grid: string;
  edge: string;
  edgeStrong: string;
  node: string;
  nodeStrong: string;
  label: string;
  labelMuted: string;
  labelHalo: string;
  plate: string;
  plateLine: string;
  accent: string;
  accentSoft: string;
  added: string;
  changed: string;
  removed: string;
  fontFamily: string;
}

/** Light-theme values, used for any token the stylesheet does not define. */
export const FALLBACK_PALETTE: Palette = {
  canvas: "#f4f5f7",
  grid: "#e3e5ea",
  edge: "#c9ccd4",
  edgeStrong: "#5f6472",
  node: "#8a8f9c",
  nodeStrong: "#3d4250",
  label: "#0f1115",
  labelMuted: "#5f6472",
  labelHalo: "#f4f5f7",
  plate: "#ffffff",
  plateLine: "#d3d5db",
  accent: "#5a55d6",
  accentSoft: "#a9a5ee",
  added: "#079455",
  changed: "#c4620a",
  removed: "#d92d20",
  fontFamily: "ui-sans-serif, system-ui, sans-serif",
};

const TOKENS: Readonly<Record<keyof Palette, string>> = {
  canvas: "--graph-canvas",
  grid: "--graph-grid",
  edge: "--graph-edge",
  edgeStrong: "--graph-edge-strong",
  node: "--graph-node",
  nodeStrong: "--graph-node-strong",
  label: "--graph-label",
  labelMuted: "--graph-label-muted",
  labelHalo: "--graph-canvas",
  plate: "--surface",
  plateLine: "--line-strong",
  accent: "--brand",
  accentSoft: "--graph-ghost",
  added: "--pass-solid",
  changed: "--warn-solid",
  removed: "--fail-solid",
  fontFamily: "--font-sans",
};

/** Resolves every palette entry through `read` (a custom-property lookup), falling back per entry. */
export function readPalette(read: (token: string) => string): Palette {
  const entries = (Object.keys(TOKENS) as (keyof Palette)[]).map((key) => {
    const value = read(TOKENS[key]).trim();
    return [key, value === "" ? FALLBACK_PALETTE[key] : value] as const;
  });
  return Object.fromEntries(entries) as unknown as Palette;
}
