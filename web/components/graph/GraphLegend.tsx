import { kindOf, type Shape } from "@/lib/graph/kinds";
import type { ExplorerNode } from "@/lib/graph/model";

const SIZE = 12;
const C = SIZE / 2;

function points(sides: number, r: number, rotation: number, dy = 0): string {
  return Array.from({ length: sides }, (_, i) => {
    const a = rotation + (i / sides) * Math.PI * 2;
    return `${(C + Math.cos(a) * r).toFixed(2)},${(C + dy + Math.sin(a) * r).toFixed(2)}`;
  }).join(" ");
}

/** The same shapes the canvas draws, as tiny SVG glyphs. */
export function ShapeGlyph({ shape }: { shape: Shape }) {
  const glyph = (() => {
    switch (shape) {
      case "ring":
        return <circle cx={C} cy={C} r={4.5} strokeWidth={3} fill="none" />;
      case "hexagon":
        return <polygon points={points(6, 5, Math.PI / 6)} />;
      case "diamond":
        return <polygon points={points(4, 5, -Math.PI / 2)} />;
      case "square":
        return <polygon points={points(4, 5.2, Math.PI / 4)} />;
      case "triangle":
        return <polygon points={points(3, 5.4, -Math.PI / 2, 0.8)} />;
      case "pentagon":
        return <polygon points={points(5, 5, -Math.PI / 2)} />;
      case "circle":
        return <circle cx={C} cy={C} r={4.5} />;
    }
  })();
  return (
    <svg className={`gx-glyph shape-${shape}`} width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`} aria-hidden="true" focusable="false">
      {glyph}
    </svg>
  );
}

const STATUS = [
  { word: "Added", glyph: "+", tone: "added" },
  { word: "Changed", glyph: "Δ", tone: "changed" },
  { word: "Removed", glyph: "×", tone: "removed" },
] as const;

/** The node kinds present in this graph, each with its shape and kind tag, and (After Play) the status marks in words. */
export function GraphLegend({ nodes, showStatus = false }: { nodes: readonly ExplorerNode[]; showStatus?: boolean }) {
  const byTag = new Map<string, string>();
  for (const n of [...nodes].sort((a, b) => kindOf(b.type).rank - kindOf(a.type).rank || a.type.localeCompare(b.type))) if (!byTag.has(n.kindTag)) byTag.set(n.kindTag, n.type);
  const kinds = [...byTag].map(([tag, type]) => ({ tag, type }));
  if (kinds.length === 0) return null;
  return (
    <ul className="gx-legend" aria-label="Node kinds">
      {kinds.map(({ tag, type }) => (
        <li key={tag} data-region={kindOf(type).region}>
          <ShapeGlyph shape={kindOf(type).shape} />
          {tag}
        </li>
      ))}
      {showStatus
        ? STATUS.map((st) => (
            <li key={st.word} className={`gx-status tone-${st.tone}`}>
              <span className="gx-badge" aria-hidden="true">
                {st.glyph}
              </span>
              {st.word}
            </li>
          ))
        : null}
    </ul>
  );
}
