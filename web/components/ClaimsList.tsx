"use client";

import type { StateField } from "@/lib/api/types";
import { formatValue } from "@/lib/format";
import { claimSelection, type Selection } from "@/lib/view/provenance";

interface Props {
  fields: Readonly<Record<string, StateField>>;
  selectedId: string | null;
  onSelect: (selection: Selection) => void;
}

/** Account-state fields that are known and carry evidence: each one is a claim the user can open. */
export function ClaimsList({ fields, selectedId, onSelect }: Props) {
  const claims = Object.entries(fields).filter(([, f]) => f.known && f.evidence_refs.length > 0);
  if (claims.length === 0) return <p className="empty">No claims with evidence yet.</p>;
  return (
    <ul className="claims">
      {claims.map(([path, field]) => {
        const sel = claimSelection(path, field);
        return (
          <li key={path}>
            <button type="button" aria-label={`Claim ${path}`} aria-pressed={selectedId === sel.id} onClick={() => onSelect(sel)}>
              <span className="path">{path}</span>
              <span className="value">{formatValue(field.value)}</span>
              <span className="standing">{field.standing}</span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
