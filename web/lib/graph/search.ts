// Find a node: case- and accent-insensitive, ranked label prefix > word prefix > anywhere (label, kind or a
// claim's value). A withheld node is findable only by its kind: its hidden label never matches.
import { kindOf } from "./kinds";
import type { ExplorerNode } from "./model";

export const SEARCH_LIMIT = 8;

export interface SearchHit {
  id: string;
  name: string;
  score: number;
  /** The kind's importance: breaks ties so a person comes before an activity that mentions them. */
  rank: number;
}

/** Lower case, accents stripped, whitespace collapsed. */
export function normalizeText(text: string): string {
  return text.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase().replace(/\s+/g, " ").trim();
}

const LABEL_PREFIX = 3;
const WORD_PREFIX = 2;
const ANYWHERE = 1;

function scoreOf(node: ExplorerNode, q: string): number {
  const label = node.withheld ? "" : normalizeText(node.label);
  if (label.startsWith(q)) return LABEL_PREFIX;
  if (label.split(" ").some((w) => w.startsWith(q))) return WORD_PREFIX;
  const hay = node.withheld ? normalizeText(node.type) : `${label} ${normalizeText(node.type)} ${normalizeText(node.detail ?? "")}`;
  return hay.includes(q) ? ANYWHERE : 0;
}

export function searchNodes(nodes: readonly ExplorerNode[], query: string, limit = SEARCH_LIMIT): SearchHit[] {
  const q = normalizeText(query);
  if (q === "") return [];
  const hits: SearchHit[] = [];
  for (const node of nodes) {
    const score = scoreOf(node, q);
    if (score > 0) hits.push({ id: node.id, name: node.name, score, rank: kindOf(node.type).rank });
  }
  return hits.sort((a, b) => b.score - a.score || b.rank - a.rank || a.name.localeCompare(b.name) || a.id.localeCompare(b.id)).slice(0, limit);
}
