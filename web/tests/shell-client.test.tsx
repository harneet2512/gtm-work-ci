// @vitest-environment jsdom
// The shell's client pieces (HAR-145): the demo flag round-trips through the URL and <html>, the
// inspector collapse persists, and the manifest form validates before it navigates.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const nav = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn(), search: "", path: "/runs" as string | null }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: nav.replace, push: nav.push, refresh: vi.fn() }),
  usePathname: () => nav.path,
  useSearchParams: () => new URLSearchParams(nav.search),
}));

const { DemoToggle } = await import("@/components/shell/DemoToggle");
const { InspectorFrame } = await import("@/components/shell/InspectorFrame");
const { ControlManifestForm } = await import("@/components/control/ControlManifestForm");

beforeEach(() => {
  nav.replace.mockClear();
  nav.push.mockClear();
  nav.search = "";
  nav.path = "/runs";
  window.localStorage.clear();
  delete document.documentElement.dataset.demo;
});
afterEach(cleanup);

describe("DemoToggle", () => {
  it("is off by default and turning it on adds ?demo=1 while keeping other params", () => {
    nav.search = "mode=trace";
    render(<DemoToggle />);
    const btn = screen.getByRole("button", { name: "Demo" });
    expect(btn.getAttribute("aria-pressed")).toBe("false");
    expect(document.documentElement.dataset.demo).toBe("");
    fireEvent.click(btn);
    expect(nav.replace).toHaveBeenCalledWith("/runs?mode=trace&demo=1", { scroll: false });
  });

  it("when on, marks <html data-demo> and turning it off drops only the demo param", () => {
    nav.search = "demo=1";
    render(<DemoToggle />);
    const btn = screen.getByRole("button", { name: "Demo" });
    expect(btn.getAttribute("aria-pressed")).toBe("true");
    expect(btn.className).toContain("on");
    expect(document.documentElement.dataset.demo).toBe("1");
    fireEvent.click(btn);
    expect(nav.replace).toHaveBeenCalledWith("/runs", { scroll: false });
  });

  it("remembers demo mode across navigation: a page opened without ?demo restores it from the stored flag", () => {
    window.localStorage.setItem("ghost.demo", "1");
    nav.search = "mode=trace";
    render(<DemoToggle />);
    expect(nav.replace).toHaveBeenCalledWith("/runs?mode=trace&demo=1", { scroll: false });
  });

  it("stores the flag when ?demo=1 is present and clears it when turned off", () => {
    nav.search = "demo=1";
    render(<DemoToggle />);
    expect(window.localStorage.getItem("ghost.demo")).toBe("1");
    expect(nav.replace).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Demo" }));
    expect(window.localStorage.getItem("ghost.demo")).toBeNull();
  });

  it("does not restore anything when the flag was never stored", () => {
    render(<DemoToggle />);
    expect(nav.replace).not.toHaveBeenCalled();
  });

  it("treats a missing pathname as the root", () => {
    nav.path = null;
    render(<DemoToggle />);
    fireEvent.click(screen.getByRole("button", { name: "Demo" }));
    expect(nav.replace).toHaveBeenCalledWith("/?demo=1", { scroll: false });
  });
});

describe("InspectorFrame", () => {
  it("renders nothing when the slot is empty", () => {
    const { container } = render(<InspectorFrame>{null}</InspectorFrame>);
    expect(container.innerHTML).toBe("");
  });

  it("shows its children expanded by default and collapses, persisting the choice", () => {
    render(
      <InspectorFrame>
        <p>detail</p>
      </InspectorFrame>,
    );
    const toggle = screen.getByRole("button", { name: "Collapse inspector" });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(toggle);
    expect(window.localStorage.getItem("ghost.inspector.collapsed")).toBe("1");
    const again = screen.getByRole("button", { name: "Expand inspector" });
    expect(again.getAttribute("aria-expanded")).toBe("false");
    expect(document.querySelector(".inspector-body")!.hasAttribute("hidden")).toBe(true);
    fireEvent.click(again);
    expect(window.localStorage.getItem("ghost.inspector.collapsed")).toBe("0");
  });

  it("starts collapsed when a previous route left it collapsed", async () => {
    window.localStorage.setItem("ghost.inspector.collapsed", "1");
    await act(async () => {
      render(
        <InspectorFrame>
          <p>detail</p>
        </InspectorFrame>,
      );
    });
    expect(screen.getByRole("button", { name: "Expand inspector" })).toBeTruthy();
    expect(document.querySelector("aside")!.className).toContain("collapsed");
    expect(document.querySelector("aside")!.className).not.toContain("unhydrated");
  });
});

describe("ControlManifestForm", () => {
  const ID = "0d3a0000-0000-4000-8000-000000000501";

  it("rejects a non-uuid without navigating", () => {
    render(<ControlManifestForm />);
    fireEvent.change(screen.getByLabelText("Demo manifest id"), { target: { value: "nope" } });
    fireEvent.submit(screen.getByRole("button", { name: "Open control" }).closest("form")!);
    expect(screen.getByRole("alert").textContent).toContain("is a uuid");
    expect(nav.push).not.toHaveBeenCalled();
  });

  it("navigates to the target with a trimmed, lower-cased id and clears a prior error", () => {
    render(<ControlManifestForm target="/system" submitLabel="Check leaks" />);
    const input = screen.getByLabelText("Demo manifest id");
    const form = screen.getByRole("button", { name: "Check leaks" }).closest("form")!;
    fireEvent.change(input, { target: { value: "bad" } });
    fireEvent.submit(form);
    expect(screen.queryByRole("alert")).not.toBeNull();
    fireEvent.change(input, { target: { value: `  ${ID.toUpperCase()}  ` } });
    fireEvent.submit(form);
    expect(nav.push).toHaveBeenCalledWith(`/system?manifest=${ID}`);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("is prefilled from initial", () => {
    render(<ControlManifestForm initial={ID} />);
    expect((screen.getByLabelText("Demo manifest id") as HTMLInputElement).value).toBe(ID);
  });
});
