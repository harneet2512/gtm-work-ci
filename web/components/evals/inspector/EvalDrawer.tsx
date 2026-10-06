"use client";

// The eval detail drawer (HAR-149 section 3): the nine sections in order, opened from a node of the episode path. Esc and the
// close button dismiss it; focus moves to it on open. The only motion in the inspector is this drawer sliding in.
import { type ReactNode, useEffect, useRef } from "react";
import type { DrawerModel } from "@/lib/evals/inspector/drawer-model";
import { DRAWER_SECTIONS } from "@/lib/evals/inspector/drawer-model";
import {
  EffectSection,
  FailuresSection,
  HappenedSection,
  HowSection,
  InputsSection,
  PerformanceSection,
  ResultPill,
  Section,
  WhatSection,
  WhenSection,
  WhySection,
} from "./DrawerSections";

export function EvalDrawer({ model, demo, onClose }: { model: DrawerModel; demo: boolean; onClose: () => void }) {
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    ref.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [model.gate, onClose]);

  const body: Record<(typeof DRAWER_SECTIONS)[number]["id"], ReactNode> = {
    what: <WhatSection m={model} />,
    why: <WhySection m={model} />,
    when: <WhenSection m={model} />,
    inputs: <InputsSection m={model} demo={demo} />,
    how: <HowSection m={model} />,
    happened: <HappenedSection m={model} />,
    effect: <EffectSection m={model} demo={demo} />,
    performance: <PerformanceSection m={model} />,
    failures: <FailuresSection m={model} demo={demo} />,
  };

  return (
    <aside ref={ref} tabIndex={-1} className="eval-drawer" role="dialog" aria-label={model.question} data-gate={model.gate} data-testid="eval-drawer">
      <header className="dr-head">
        <div>
          <p className="dr-meta">
            <span className="mono">{model.gate}</span>
            <span>{model.name}</span>
            <span>{model.episodeLabel}</span>
          </p>
          <h2>{model.question}</h2>
          <p className="dr-verdict">
            <ResultPill result={model.happened.verdict} />
            <span>{model.measured ? "in this episode" : "has not run in this episode"}</span>
          </p>
        </div>
        <button type="button" className="dr-close" onClick={onClose} aria-label="Close">
          ×
        </button>
      </header>
      <div className="dr-body">
        {DRAWER_SECTIONS.map((s) => (
          <Section key={s.id} id={s.id} title={s.title}>
            {body[s.id]}
          </Section>
        ))}
      </div>
    </aside>
  );
}
