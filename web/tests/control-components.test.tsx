// @vitest-environment jsdom
// The /control components (HAR-145): the bands, material changes and trajectory render honest states
// (the Play strip is covered by pipeline-strip.test.tsx).
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { ControlView } from "@/lib/view/control";

const { HealthBands, MaterialChanges, TrajectoryRail } = await import("@/components/control/ControlSections");

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const EPISODE = "0de50000-0000-4000-8000-000000000202";

afterEach(() => {
  cleanup();
});

describe("HealthBands", () => {
  it("renders each band's tone word and omits the fact list when empty", () => {
    render(
      <HealthBands
        bands={[
          { id: "intelligence", label: "Intelligence", status: "ok", tone: "ok", facts: ["1 material event"] },
          { id: "decision_learning", label: "Decision & Learning", status: "bad", tone: "fail", facts: [] },
          { id: "cliff_experience", label: "Cliff / Experience", status: "not observable", tone: "none", facts: [] },
          { id: "system", label: "System", status: "careful", tone: "warn", facts: [] },
          { id: "intelligence", label: "Intelligence", status: "unsure", tone: "unsure", facts: [] },
        ]}
      />,
    );
    expect([...document.querySelectorAll(".sr-only")].map((e) => e.textContent)).toEqual(["healthy", "failing", "no signal", "warning", "unsure"]);
    expect(document.querySelectorAll("li")).toHaveLength(1);
  });

  it("shows a check mark only for a real eval verdict tone; recorded and unsure get their own neutral marks", () => {
    render(
      <HealthBands
        bands={[
          { id: "cliff_experience", label: "Cliff / Experience", status: "1 message posted", tone: "recorded", facts: [] },
          { id: "intelligence", label: "Intelligence", status: "unsure", tone: "unsure", facts: [] },
          { id: "decision_learning", label: "Decision & Learning", status: "ok", tone: "ok", facts: [] },
        ]}
      />,
    );
    const marks = [...document.querySelectorAll(".band-mark")].map((e) => e.textContent);
    expect(marks).toEqual(["●", "◌", "✓"]);
    expect([...document.querySelectorAll(".sr-only")].map((e) => e.textContent)).toEqual(["recorded", "unsure", "healthy"]);
  });
});

describe("MaterialChanges", () => {
  it("says nothing changed yet when empty", () => {
    render(<MaterialChanges changes={[]} />);
    expect(document.body.textContent).toContain("No released event has changed the world yet.");
  });

  it("links an episode only when the change opened one", () => {
    render(
      <MaterialChanges
        changes={[
          { position: 1, label: "Email", detail: "state v6", decisionEpisodeId: EPISODE },
          { position: 2, label: "Call", detail: null, decisionEpisodeId: null },
        ]}
      />,
    );
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]!.getAttribute("href")).toBe(`/episodes/${EPISODE}`);
    expect(document.querySelectorAll(".detail")).toHaveLength(1);
  });

  it("carries the manifest so the episode page can tie Message 1 to this change", () => {
    render(<MaterialChanges manifestId={MANIFEST} changes={[{ position: 1, label: "Email", detail: null, decisionEpisodeId: EPISODE }]} />);
    expect(screen.getByRole("link").getAttribute("href")).toBe(`/episodes/${EPISODE}?manifest=${MANIFEST}`);
  });
});

describe("TrajectoryRail", () => {
  const steps: ControlView["trajectory"] = [
    { position: 1, label: "Email", released: true, material: true },
    { position: 2, label: "Call", released: true, material: false },
    { position: 3, label: "Note", released: true, material: null },
    { position: 4, label: "Held", released: false, material: null },
  ] as ControlView["trajectory"];

  it("in Demo mode drops the Replay link (it shows raw ids) and keeps the steps readable", () => {
    render(<TrajectoryRail steps={steps} manifestId={MANIFEST} demo />);
    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(screen.getByLabelText("Episode 1, material")).toBeTruthy();
    expect([...document.querySelectorAll(".status")].map((s) => s.textContent)).toEqual(["material", "no action", "released", "held out"]);
  });

  it("links released steps to the replay bound and leaves the held-out step inert", () => {
    render(<TrajectoryRail steps={steps} manifestId={MANIFEST} />);
    const links = screen.getAllByRole("link");
    expect(links.map((l) => l.getAttribute("href"))).toEqual([1, 2, 3].map((n) => `/replay/${MANIFEST}?at=${n}`));
    expect(links.map((l) => l.getAttribute("aria-label"))).toEqual(["Episode 1, material", "Episode 2, no action", "Episode 3, released"]);
    expect(screen.getByLabelText("Event 4, held out")).toBeTruthy();
    expect([...document.querySelectorAll(".status")].map((s) => s.textContent)).toEqual(["material", "no action", "released", "held out"]);
  });
});
