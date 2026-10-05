import type { GraphNode } from "@/lib/api/types";

export interface Point {
  x: number;
  y: number;
}

export interface Layout {
  positions: ReadonlyMap<string, Point>;
  width: number;
  height: number;
}

const COLUMN_WIDTH = 230;
const ROW_HEIGHT = 64;
const PAD_X = 90;
const PAD_Y = 44;
const MIN_ROWS = 4;

// Reading order of the map: people (who) -> account and opportunity (what) -> evidence and learning (why).
const COLUMN_OF: Record<string, number> = {
  Person: 0,
  Account: 1,
  Opportunity: 1,
  Activity: 2,
  Conversation: 2,
  Document: 2,
  Claim: 3,
  Commitment: 3,
  Signal: 3,
  DecisionEpisode: 3,
  Knowledge: 3,
};
const FALLBACK_COLUMN = 3;
const COLUMNS = 4;

export const columnOf = (type: string): number => COLUMN_OF[type] ?? FALLBACK_COLUMN;

/** Deterministic column layout: the same nodes in any order land on the same points. */
export function layoutGraph(nodes: readonly GraphNode[]): Layout {
  const columns: GraphNode[][] = Array.from({ length: COLUMNS }, () => []);
  for (const node of nodes) columns[columnOf(node.type)]!.push(node);

  const rows = Math.max(MIN_ROWS, ...columns.map((c) => c.length));
  const height = PAD_Y * 2 + (rows - 1) * ROW_HEIGHT;
  const positions = new Map<string, Point>();
  columns.forEach((members, col) => {
    const sorted = [...members].sort((a, b) => a.type.localeCompare(b.type) || a.label.localeCompare(b.label) || a.id.localeCompare(b.id));
    const span = (sorted.length - 1) * ROW_HEIGHT;
    const top = (height - span) / 2;
    sorted.forEach((node, row) => positions.set(node.id, { x: PAD_X + col * COLUMN_WIDTH, y: top + row * ROW_HEIGHT }));
  });
  return { positions, width: PAD_X * 2 + (COLUMNS - 1) * COLUMN_WIDTH, height };
}
