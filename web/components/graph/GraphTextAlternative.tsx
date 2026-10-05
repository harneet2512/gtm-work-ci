"use client";

import type { ExplorerModel } from "@/lib/graph/model";

interface Props {
  model: ExplorerModel;
  selectedId: string | null;
  onNode: (id: string) => void;
  onEdge: (id: string) => void;
}

const suffix = (mark: string | undefined): string => (mark ? `, ${mark}` : "");

/**
 * The graph as text: every node and edge as a button, for screen readers and keyboard users. It is visually
 * hidden until something in it has keyboard focus, then it shows as a plain list over the canvas.
 */
export function GraphTextAlternative({ model, selectedId, onNode, onEdge }: Props) {
  const nameOf = (id: string): string => model.byId.get(id)?.label ?? id;
  return (
    <div className="gx-text">
      <h3>{`Nodes (${model.nodes.length})`}</h3>
      <ul>
        {model.nodes.map((n) => (
          <li key={n.id}>
            <button
              type="button"
              data-node-id={n.id}
              data-mark={n.mark}
              aria-pressed={selectedId === n.id}
              aria-label={`${n.name}${suffix(n.mark)}`}
              onClick={() => onNode(n.id)}
            >
              {`${n.name}${suffix(n.mark)}`}
            </button>
          </li>
        ))}
      </ul>
      <h3>{`Edges (${model.edges.length})`}</h3>
      <ul>
        {model.edges.map((e) => {
          const name = `${e.label}: ${nameOf(e.source)} → ${nameOf(e.target)}${suffix(e.mark)}`;
          return (
            <li key={e.id}>
              <button
                type="button"
                data-edge-id={e.id}
                data-mark={e.mark}
                aria-pressed={selectedId === e.id}
                aria-label={name}
                onClick={() => onEdge(e.id)}
              >
                {name}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
