"use client";

import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { ExplorerNode } from "@/lib/graph/model";
import { searchNodes } from "@/lib/graph/search";

interface Props {
  nodes: readonly ExplorerNode[];
  onJump: (id: string) => void;
}

const isMac = (): boolean => typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

/** Find a node and jump to it. Ctrl+K (Cmd+K on a Mac) focuses it from anywhere on the page. */
export function GraphSearch({ nodes, onJump }: Props) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [open, setOpen] = useState(false);
  const [shortcut, setShortcut] = useState("Ctrl K");
  const input = useRef<HTMLInputElement | null>(null);
  const listId = useId();
  const hits = useMemo(() => searchNodes(nodes, query), [nodes, query]);

  useEffect(() => {
    if (isMac()) setShortcut("⌘K");
    const onKey = (e: globalThis.KeyboardEvent): void => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        input.current?.focus();
        input.current?.select();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const jump = (id: string): void => {
    setOpen(false);
    setQuery("");
    onJump(id);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (hits.length > 0) setActive((i) => (i + (e.key === "ArrowDown" ? 1 : -1) + hits.length) % hits.length);
      setOpen(true);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const hit = hits[active];
      if (hit) jump(hit.id);
    } else if (e.key === "Escape") {
      e.stopPropagation();
      if (query !== "") setQuery("");
      else input.current?.blur();
      setOpen(false);
    }
  };

  const expanded = open && query.trim() !== "";
  return (
    <div className="gx-search">
      <input
        ref={input}
        type="search"
        role="combobox"
        aria-label="Find a node in the graph"
        aria-expanded={expanded}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={expanded && hits[active] ? `${listId}-${active}` : undefined}
        placeholder="Find a node"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setActive(0);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onKeyDown={onKeyDown}
      />
      <kbd aria-hidden="true">{shortcut}</kbd>
      <ul id={listId} role="listbox" aria-label="Matching nodes" className="gx-results" hidden={!expanded}>
        {hits.length === 0 ? (
          <li role="presentation" className="gx-noresult">
            No node matches.
          </li>
        ) : (
          hits.map((hit, i) => (
            <li
              key={hit.id}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => jump(hit.id)}
              onMouseEnter={() => setActive(i)}
            >
              {hit.name}
            </li>
          ))
        )}
      </ul>
    </div>
  );
}
