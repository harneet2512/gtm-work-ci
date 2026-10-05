// Test helpers for the token file: read the hex custom properties of one selector block and compute the
// WCAG 2.x contrast ratio between two colors.

/** The `--name: #hex;` declarations of the first block whose selector is exactly `selector`. */
export function parseTokens(css: string, selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) return {};
  const end = css.indexOf("}", start);
  const block = css.slice(start, end);
  const out: Record<string, string> = {};
  for (const m of block.matchAll(/(--[\w-]+):\s*(#[0-9a-fA-F]{3,8})\s*;/g)) out[m[1]!] = m[2]!.toLowerCase();
  return out;
}

function channel(c: number): number {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

function luminance(hex: string): number {
  const h = hex.replace("#", "");
  const full = h.length === 3 ? [...h].map((x) => x + x).join("") : h.slice(0, 6);
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16));
  return 0.2126 * channel(r!) + 0.7152 * channel(g!) + 0.0722 * channel(b!);
}

export function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi! + 0.05) / (lo! + 0.05);
}
