"use client";

// Binds the imperative engine to React: one engine per model (a new read rebuilds it on top of the
// remembered layout), the theme (palette from the app tokens, live on theme change), reduced motion and
// the canvas size. Callbacks are read through a ref, so changing them never rebuilds the engine.
import { useEffect, useRef, type RefObject } from "react";
import { layoutMemory } from "./memory";
import type { ExplorerModel } from "./model";
import { readPalette, type Palette } from "./palette";
import { createGraphEngine, type EngineCallbacks, type GraphEngine } from "./render/engine";

export interface GraphEngineRefs {
  canvasRef: RefObject<HTMLCanvasElement | null>;
  hostRef: RefObject<HTMLDivElement | null>;
  engineRef: RefObject<GraphEngine | null>;
}

export function hostPalette(host: Element): Palette {
  const style = getComputedStyle(host);
  return readPalette((token) => style.getPropertyValue(token));
}

const REDUCED = "(prefers-reduced-motion: reduce)";

export function useGraphEngine(model: ExplorerModel, callbacks: EngineCallbacks): GraphEngineRefs {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const hostRef = useRef<HTMLDivElement | null>(null);
  const engineRef = useRef<GraphEngine | null>(null);
  const latest = useRef(callbacks);
  useEffect(() => {
    latest.current = callbacks;
  });

  useEffect(() => {
    const canvas = canvasRef.current;
    const host = hostRef.current;
    if (!canvas || !host) return;
    const media = typeof window.matchMedia === "function" ? window.matchMedia(REDUCED) : null;
    const engine = createGraphEngine({
      canvas,
      host,
      probeRoot: host.closest(".gx") ?? host,
      model,
      palette: hostPalette(host),
      reduced: media?.matches ?? false,
      memory: layoutMemory(),
      callbacks: {
        onPick: (id) => latest.current.onPick(id),
        onDive: (id) => latest.current.onDive(id),
        onBackground: () => latest.current.onBackground(),
      },
    });
    engineRef.current = engine;

    const onMotion = (): void => engine.setReduced(media?.matches ?? false);
    media?.addEventListener("change", onMotion);
    const resize = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => engine.resize());
    resize?.observe(host);
    const theme = new MutationObserver(() => engine.setPalette(hostPalette(host)));
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "class"] });
    let live = true;
    // Labels are measured with the app font: redraw once it has loaded.
    void document.fonts?.ready.then(() => {
      if (live) engine.resize();
    });

    return () => {
      live = false;
      media?.removeEventListener("change", onMotion);
      resize?.disconnect();
      theme.disconnect();
      engine.destroy();
      engineRef.current = null;
    };
  }, [model]);

  return { canvasRef, hostRef, engineRef };
}
