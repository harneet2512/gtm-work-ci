"use client";

import type { KeyboardEvent } from "react";
import type { Graph, GraphNode } from "@/lib/api/types";
import { shortId } from "@/lib/format";
import type { DiffIndex, DiffOp } from "@/lib/view/diff";
import { layoutGraph } from "@/lib/view/layout";
import { edgeSelection, nodeSelection, type Selection } from "@/lib/view/provenance";

interface Props {
  graph: Graph;
  marks: DiffIndex;
  /** Highlight what the Play event changed (After Play only). */
  showMarks: boolean;
  selectedId: string | null;
  onSelect: (selection: Selection) => void;
}

const NODE_W = 156;
const NODE_H = 34;
const MAX_LABEL = 22;

const clip = (text: string): string => (text.length > MAX_LABEL ? `${text.slice(0, MAX_LABEL - 1)}…` : text);

function activate(event: KeyboardEvent, action: () => void): void {
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    action();
  }
}

function nodeLabel(node: GraphNode): string {
  return node.withheld ? "withheld by visibility" : node.label;
}

export function AccountMap({ graph, marks, showMarks, selectedId, onSelect }: Props) {
  if (graph.nodes.length === 0) return <p className="empty">No graph around this account yet.</p>;

  const { positions, width, height } = layoutGraph(graph.nodes);
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const labelOf = (id: string): string => {
    const node = byId.get(id);
    return node ? nodeLabel(node) : shortId(id);
  };
  const mark = (op: DiffOp | undefined): DiffOp | undefined => (showMarks ? op : undefined);
  const suffix = (op: DiffOp | undefined): string => (op ? `, ${op}` : "");

  return (
    <svg className="map" viewBox={`0 0 ${width} ${height}`} role="group" aria-label="Account map" data-testid="account-map">
      <g className="map-edges">
        {graph.edges.map((edge) => {
          const from = positions.get(edge.source);
          const to = positions.get(edge.target);
          if (!from || !to) return null;
          const op = mark(marks.edges.get(edge.id));
          const sel = edgeSelection(edge, labelOf(edge.source), labelOf(edge.target));
          const pick = () => onSelect(sel);
          return (
            <g
              key={edge.id}
              role="button"
              tabIndex={0}
              aria-label={`${sel.title}${suffix(op)}`}
              aria-pressed={selectedId === edge.id}
              data-edge-id={edge.id}
              data-mark={op}
              className={`edge${selectedId === edge.id ? " selected" : ""}`}
              onClick={pick}
              onKeyDown={(e) => activate(e, pick)}
            >
              <line x1={from.x} y1={from.y} x2={to.x} y2={to.y} className="edge-hit" />
              <line x1={from.x} y1={from.y} x2={to.x} y2={to.y} className="edge-line" />
              <text x={(from.x + to.x) / 2} y={(from.y + to.y) / 2 - 3} className="edge-label">
                {edge.rel_type}
              </text>
            </g>
          );
        })}
      </g>
      <g className="map-nodes">
        {graph.nodes.map((node) => {
          const at = positions.get(node.id)!;
          const op = mark(marks.nodes.get(node.id));
          const sel = nodeSelection(node);
          const pick = () => onSelect(sel);
          return (
            <g
              key={node.id}
              role="button"
              tabIndex={0}
              aria-label={`${node.type}: ${nodeLabel(node)}${suffix(op)}`}
              aria-pressed={selectedId === node.id}
              data-node-id={node.id}
              data-mark={op}
              className={`node type-${node.type}${node.withheld ? " withheld" : ""}${selectedId === node.id ? " selected" : ""}`}
              transform={`translate(${at.x - NODE_W / 2} ${at.y - NODE_H / 2})`}
              onClick={pick}
              onKeyDown={(e) => activate(e, pick)}
            >
              <rect width={NODE_W} height={NODE_H} rx={7} />
              <text x={NODE_W / 2} y={13} className="node-type">
                {node.type}
              </text>
              <text x={NODE_W / 2} y={27} className="node-label">
                {clip(nodeLabel(node))}
              </text>
            </g>
          );
        })}
      </g>
    </svg>
  );
}
