// The Play strip keeps its own state (stages, line, polling token). When the page opens another account (?manifest=)
// that state must not survive, so the strip is keyed on the manifest.
import { isValidElement, type ReactElement, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { PipelineStrip } from "@/components/control/PipelineStrip";

vi.mock("@/lib/cached-loaders", () => ({
  getControlPage: vi.fn(async () => ({
    replay: { notices: [], view: {} },
    control: { accountName: "A", episode: 1, total: 2, window: "w", stateVersion: 1, run: null, opportunityId: null, canPlayNext: true, nextEvent: null, progress: null, bands: [], changes: [], manifestId: "m", trajectory: [] },
  })),
}));
vi.mock("@/app/control/actions", () => ({ playEvent: vi.fn() }));

const { default: ControlPage } = await import("@/app/control/page");

function find(node: ReactNode, type: unknown): ReactElement | null {
  if (!isValidElement(node)) return Array.isArray(node) ? node.map((n) => find(n, type)).find(Boolean) ?? null : null;
  if (node.type === type) return node;
  return find((node.props as { children?: ReactNode }).children, type);
}

const render = async (manifest: string) => ControlPage({ searchParams: Promise.resolve({ manifest }) });

describe("/control", () => {
  it("keys the Play strip on the manifest so its state resets when the account changes", async () => {
    const a = "0d3a0000-0000-4000-8000-000000000501";
    const b = "0d3a0000-0000-4000-8000-000000000502";
    expect(find(await render(a), PipelineStrip)?.key).toBe(a);
    expect(find(await render(b), PipelineStrip)?.key).toBe(b);
  });
});
