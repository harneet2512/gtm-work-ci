// @vitest-environment jsdom
// The app shell: which section a route belongs to, the nav marking it, and the brand.
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { activeSection, NAV_GROUPS, SECTIONS } from "@/lib/view/nav";

const pathname = vi.hoisted(() => ({ value: "/", search: "" }));
vi.mock("next/navigation", () => ({ usePathname: () => pathname.value, useSearchParams: () => new URLSearchParams(pathname.search) }));

const { RailNav } = await import("@/components/shell/RailNav");
const { BrandMark } = await import("@/components/shell/BrandMark");

afterEach(() => {
  cleanup();
  pathname.search = "";
});

describe("activeSection", () => {
  it.each([
    ["/", "/"],
    ["/accounts/0a0c0000-0000-4000-8000-000000000001", "/"],
    ["/control", "/control"],
    ["/replay", "/replay"],
    ["/replay/0d3a0000-0000-4000-8000-000000000501", "/replay"],
    ["/runs", "/runs"],
    ["/runs/0f0a0000-0000-4000-8000-000000000601/evals", "/runs"],
    ["/knowledge/0c17c000-0000-4000-8000-000000000017", "/knowledge"],
    ["/evals", "/evals"],
  ])("%s belongs to %s", (path, section) => {
    expect(activeSection(path)).toBe(section);
  });

  it("a route of no section marks none", () => {
    expect(activeSection("/nowhere")).toBeNull();
    expect(activeSection("/runsabc")).toBeNull();
  });

  it("lists the sections in order, Control first", () => {
    expect(SECTIONS.map((s) => s.label)).toEqual(["Control", "Accounts", "Replay", "Runs", "Knowledge", "Evals", "System"]);
  });

  it("groups the rail by product area", () => {
    expect(NAV_GROUPS.map((g) => g.label)).toEqual([null, "Intelligence", "Decision & Learning", null]);
    expect(NAV_GROUPS[1]!.items.map((i) => i.label)).toEqual(["Accounts", "Replay"]);
    expect(NAV_GROUPS[2]!.items.map((i) => i.label)).toEqual(["Runs", "Knowledge", "Evals"]);
    expect(NAV_GROUPS[3]!.items.map((i) => i.label)).toEqual(["System"]);
  });
});

describe("RailNav", () => {
  it("marks only the current section with aria-current", () => {
    pathname.value = "/runs/0f0a0000-0000-4000-8000-000000000601/evals";
    render(<RailNav />);
    const nav = screen.getByRole("navigation", { name: "Sections" });
    const current = [...nav.querySelectorAll("a[aria-current='page']")].map((a) => a.textContent);
    expect(current).toEqual(["Runs"]);
    expect(screen.getByRole("link", { name: "Evals" }).getAttribute("href")).toBe("/evals");
  });

  it("keeps ?demo=1 on every rail link while demo mode is on, and adds nothing when it is off", () => {
    pathname.value = "/control";
    pathname.search = "demo=1&manifest=m1";
    render(<RailNav />);
    const hrefs = [...document.querySelectorAll(".railnav a")].map((a) => a.getAttribute("href"));
    expect(hrefs).toContain("/runs?demo=1");
    expect(hrefs.every((h) => h!.endsWith("?demo=1"))).toBe(true);
    cleanup();
    pathname.search = "";
    render(<RailNav />);
    expect([...document.querySelectorAll(".railnav a")].map((a) => a.getAttribute("href"))).toContain("/runs");
  });

  it("marks nothing on an unknown route", () => {
    pathname.value = "/nowhere";
    render(<RailNav />);
    expect(document.querySelectorAll("a[aria-current]")).toHaveLength(0);
  });
});

describe("BrandMark", () => {
  it("is decorative: the wordmark beside it names the product", () => {
    const { container } = render(<BrandMark />);
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("focusable")).toBe("false");
  });
});
