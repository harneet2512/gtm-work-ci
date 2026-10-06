import { expect, test, type Page } from "@playwright/test";

// The live account graph explorer on the real MedTech Advances case (fixture core): zoom, click to inspect,
// Escape, stepping through time, search, drag, and Before -> After applying the event's graph diff on top of
// the same layout. The canvas is driven with the mouse and keyboard; node screen points come from the
// text alternative (each node's twin carries data-sx / data-sy once the picture is still).
const ACCOUNT = "0a0cad00-0000-4000-8000-00000000ad01";
const EVENT = "05e0ad00-0000-4000-8000-00000000ad0d";
const PAGE = `/accounts/${ACCOUNT}?event=${EVENT}`;
const FATOUMATA = "0b0ead00-0000-4000-8000-00000000ad11";
const ACCOUNT_NODE = ACCOUNT;

const errors: string[] = [];

test.beforeEach(({ page }) => {
  errors.length = 0;
  page.on("console", (msg) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  page.on("pageerror", (err) => errors.push(err.message));
});

test.afterEach(() => {
  expect(errors, "no console errors").toEqual([]);
});

const stage = (page: Page) => page.locator(".gx-stage");
const zoomOf = async (page: Page): Promise<number> => Number(await stage(page).getAttribute("data-zoom"));

async function settled(page: Page): Promise<void> {
  await expect(stage(page)).toHaveAttribute("data-settled", "true", { timeout: 15_000 });
}

/** The page point of a node on the canvas. */
async function nodePoint(page: Page, id: string): Promise<{ x: number; y: number }> {
  const twin = page.locator(`[data-node-id="${id}"]`);
  await expect(twin).toHaveAttribute("data-sx", /^-?\d+$/);
  const box = (await page.locator("canvas.gx-canvas").boundingBox())!;
  return { x: box.x + Number(await twin.getAttribute("data-sx")), y: box.y + Number(await twin.getAttribute("data-sy")) };
}

/** The world point of a node (layout coordinates, independent of the camera). */
async function worldPoint(page: Page, id: string): Promise<{ x: number; y: number }> {
  const twin = page.locator(`[data-node-id="${id}"]`);
  await expect(twin).toHaveAttribute("data-wx", /^-?\d+\.\d+$/);
  return { x: Number(await twin.getAttribute("data-wx")), y: Number(await twin.getAttribute("data-wy")) };
}

const provenance = (page: Page) => page.getByRole("region", { name: "Provenance" });

test("zooms with the wheel and the buttons, and fits everything back", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  const fitted = await zoomOf(page);
  const box = (await page.locator("canvas.gx-canvas").boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, -500);
  await expect.poll(() => zoomOf(page)).toBeGreaterThan(fitted * 1.2);
  await page.getByRole("button", { name: "Fit the whole graph" }).click();
  await expect.poll(() => zoomOf(page)).toBeCloseTo(fitted, 1);
  await page.getByRole("button", { name: "Zoom in" }).click();
  await expect.poll(() => zoomOf(page)).toBeGreaterThan(fitted);
  await page.getByRole("button", { name: "Zoom out" }).click();
  await expect.poll(() => zoomOf(page)).toBeCloseTo(fitted, 1);
  await expect(page.locator("[data-hud-zoom]")).toHaveText(/^\d+%$/);
  // The page fills no inspector slot, so the shell shows no empty drawer next to the graph.
  await expect(page.getByRole("complementary", { name: "Inspector" })).toBeHidden();
});

test("clicking a node flies to it and opens its evidence; Escape backs out", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  const fitted = await zoomOf(page);
  const p = await nodePoint(page, FATOUMATA);
  await page.mouse.click(p.x, p.y);
  await expect(stage(page)).toHaveAttribute("data-focus", FATOUMATA);
  await expect(provenance(page).getByRole("heading", { name: "Fatoumata Touré" })).toBeVisible();
  await expect(provenance(page).getByRole("button", { name: "Open MedTech Advances (Account)" })).toBeVisible();
  await expect.poll(() => zoomOf(page)).toBeGreaterThanOrEqual(1.8);
  await expect(page.getByRole("navigation", { name: "Visited nodes" }).getByRole("button")).toHaveCount(1);

  // Walk the graph from the inspector: the camera follows.
  await provenance(page).getByRole("button", { name: "Open MedTech Advances (Account)" }).click();
  await expect(stage(page)).toHaveAttribute("data-focus", ACCOUNT_NODE);
  await expect(page.getByRole("navigation", { name: "Visited nodes" }).getByRole("button")).toHaveCount(2);

  await page.locator("canvas.gx-canvas").focus();
  await page.keyboard.press("Escape");
  await expect(stage(page)).toHaveAttribute("data-focus", "");
  await expect(provenance(page).getByText(/Click a node/)).toBeVisible();
  await expect.poll(() => zoomOf(page)).toBeCloseTo(fitted, 1);
});

test("double-click dives into a node", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  const p = await nodePoint(page, ACCOUNT_NODE);
  await page.mouse.dblclick(p.x, p.y);
  await expect.poll(() => zoomOf(page)).toBeGreaterThanOrEqual(3);
  await expect(stage(page)).toHaveAttribute("data-focus", ACCOUNT_NODE);
});

test("the arrow keys step through activities in time order", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  await page.locator("canvas.gx-canvas").focus();
  await page.keyboard.press("ArrowRight");
  await expect(provenance(page).getByRole("heading", { name: "CRM update · May 25, 2023" })).toBeVisible();
  await page.keyboard.press("ArrowRight");
  await expect(provenance(page).getByRole("heading", { name: "Contact added: Fatoumata Touré · Oct 15, 2023" })).toBeVisible();
  await page.keyboard.press("ArrowLeft");
  await expect(provenance(page).getByRole("heading", { name: "CRM update · May 25, 2023" })).toBeVisible();
  await expect.poll(() => zoomOf(page)).toBeGreaterThanOrEqual(1.8);
});

test("Ctrl+K finds a node and jumps to it", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  await page.keyboard.press("Control+k");
  const box = page.getByRole("combobox", { name: "Find a node in the graph" });
  await expect(box).toBeFocused();
  await box.fill("toure");
  await expect(page.getByRole("option", { name: "Fatoumata Touré (Person)" })).toBeVisible();
  await box.press("Enter");
  await expect(stage(page)).toHaveAttribute("data-focus", FATOUMATA);
  await expect(provenance(page).getByRole("heading", { name: "Fatoumata Touré" })).toBeVisible();
});

test("the text alternative shows itself on keyboard focus without moving the page", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  const claims = page.getByRole("region", { name: "Claims" });
  const before = await claims.boundingBox();
  const twin = page.getByTestId("account-map").getByRole("button", { name: "Fatoumata Touré (Person)" });
  await twin.focus();
  await expect(twin).toBeInViewport();
  expect(await claims.boundingBox()).toEqual(before);
  await page.keyboard.press("Enter");
  await expect(provenance(page).getByRole("heading", { name: "Fatoumata Touré" })).toBeVisible();
  await expect(stage(page)).toHaveAttribute("data-focus", FATOUMATA);
});

test("a node can be dragged to a new place", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  const p = await nodePoint(page, FATOUMATA);
  await page.mouse.move(p.x, p.y);
  await page.mouse.down();
  await page.mouse.move(p.x + 90, p.y + 50, { steps: 10 });
  await page.mouse.up();
  await expect
    .poll(
      async () => {
        const q = await nodePoint(page, FATOUMATA);
        return Math.round(Math.hypot(q.x - p.x - 90, q.y - p.y - 50));
      },
      { timeout: 10_000 },
    )
    .toBeLessThan(4);
});

test("After Play grows the event's changes out of its activity and keeps everything else in place", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  await expect(page.getByText(/^The graph holds all 12 activities and all 7 facts on record\.$/)).toBeVisible();
  const twins = page.locator("[data-node-id]");
  const ids = await twins.evaluateAll((els) => els.map((e) => e.getAttribute("data-node-id")!));
  expect(ids).toHaveLength(23);
  const before = new Map<string, { x: number; y: number }>();
  for (const id of ids) before.set(id, await worldPoint(page, id));
  await page.evaluate(() => {
    const seen: string[] = [];
    (window as unknown as { __entrance: string[] }).__entrance = seen;
    new MutationObserver((records) => {
      for (const r of records) seen.push((r.target as HTMLElement).getAttribute("data-entrance") ?? "");
    }).observe(document.body, { subtree: true, attributes: true, attributeFilter: ["data-entrance"] });
  });

  await page.getByRole("link", { name: "After Play" }).click();
  await expect(page).toHaveURL(/view=after/);
  await expect(page.getByRole("heading", { name: "After Play: what changed" })).toBeVisible();
  await expect(stage(page)).toHaveAttribute("data-entrance", "done", { timeout: 15_000 });
  const seen = await page.evaluate(() => (window as unknown as { __entrance: string[] }).__entrance);
  expect(seen).toContain("running");

  const map = page.getByTestId("account-map");
  await expect(map.locator('[data-node-id][data-mark="added"]')).toHaveCount(6);
  await expect(map.locator('[data-edge-id][data-mark="added"]')).toHaveCount(20);
  const diff = page.getByTestId("graph-diff");
  await expect(diff.getByRole("button", { name: "Show Email from Fatoumata · Nov 9, 2023 (Activity)" })).toBeVisible();
  await expect(diff.getByRole("region", { name: "Added" }).getByRole("button", { name: /^Show derived from: Customer replied → Objection: Long-term cost/ })).toBeVisible();

  // Everything that was there Before and still holds stays exactly where it was: nothing is re-laid out.
  await settled(page);
  for (const [id, p] of before) {
    if ((await page.locator(`[data-node-id="${id}"]`).count()) === 0) continue;
    const q = await worldPoint(page, id);
    expect(Math.abs(q.x - p.x), `${id} x`).toBeLessThanOrEqual(0.01);
    expect(Math.abs(q.y - p.y), `${id} y`).toBeLessThanOrEqual(0.01);
  }

  await diff.getByRole("button", { name: "Show Email from Fatoumata · Nov 9, 2023 (Activity)" }).click();
  await expect(provenance(page).getByRole("heading", { name: "Email from Fatoumata · Nov 9, 2023" })).toBeVisible();
  await expect(provenance(page).getByText("This event")).toBeVisible();

  // The superseded use case stays as a ghost, named as it was Before, and opens from the rail.
  const ghostRow = diff.getByRole("region", { name: "Changed" }).getByRole("button", { name: /^Show Product use case: Data protection and healthcare education/ });
  await expect(ghostRow).toBeVisible();
  await ghostRow.click();
  await expect(provenance(page).getByText("Fact · no longer in the graph")).toBeVisible();
  await expect(provenance(page).getByText("status: active → superseded")).toBeVisible();

  // Temporal memory: the new use case says what it used to be, and since when.
  await map.getByRole("button", { name: /^Product use case: CryptGuard Module .*\(Fact\), added$/ }).focus();
  await page.keyboard.press("Enter");
  await expect(provenance(page).getByText(/^Data protection and healthcare education: .* \(until Nov 9, 2023\)$/)).toBeVisible();
  await expect(provenance(page).getByText("Said by Fatoumata Touré").first()).toBeVisible();
});

test("expands to fill the window, and Escape comes back", async ({ page }) => {
  await page.goto(PAGE);
  await settled(page);
  const before = (await page.locator("canvas.gx-canvas").boundingBox())!;
  await page.getByRole("button", { name: "Expand the graph" }).click();
  await expect(page.getByTestId("account-map")).toHaveClass(/is-expanded/);
  await expect.poll(async () => (await page.locator("canvas.gx-canvas").boundingBox())!.width).toBeGreaterThan(before.width);
  await page.locator("canvas.gx-canvas").focus();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("account-map")).not.toHaveClass(/is-expanded/);
  await page.keyboard.press("f");
  await expect(page.getByTestId("account-map")).toHaveClass(/is-expanded/);
});

test("with reduced motion the After Play graph is still from the first frame", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto(`${PAGE}&view=after`);
  await expect(stage(page)).toHaveAttribute("data-entrance", "done", { timeout: 15_000 });
  const canvas = page.locator("canvas.gx-canvas");
  const shots: string[] = [];
  for (let i = 0; i < 4; i += 1) {
    shots.push((await canvas.screenshot()).toString("base64"));
    await page.waitForTimeout(250);
  }
  expect(new Set(shots).size).toBe(1);
});
