// World-space geometry shared by the layout and the transition: points, the rings around the account and
// stable seeds. The graph is one connected structure: the account at the centre, the deal close beside it,
// and everything else on rings by how many links it is from the account.

export interface Point {
  x: number;
  y: number;
}

/** Distance between rings, in world units. */
export const RING = 130;
/** The deal sits closer than the first ring: it is the account's other half. */
const DEAL_RING = 0.55;

/** The radius a node is pulled to: 0 for the account, rings outward by link depth. */
export function ringRadius(depth: number, type: string): number {
  if (depth <= 0) return 0;
  return (type === "Opportunity" && depth === 1 ? DEAL_RING : depth) * RING;
}

/** FNV-1a of a string, mapped to [0, 1): a stable pseudo-random number per id. */
export function hashUnit(id: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < id.length; i += 1) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0) / 0x100000000;
}

/** A deterministic first point for a node with no history: on its ring, at an angle from its id. */
export function seedPoint(id: string, depth: number, type: string): Point {
  const r = ringRadius(depth, type);
  if (r === 0) return { x: 0, y: 0 };
  const angle = hashUnit(id) * Math.PI * 2;
  return { x: Math.cos(angle) * r, y: Math.sin(angle) * r };
}
