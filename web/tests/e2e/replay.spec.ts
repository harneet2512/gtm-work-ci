import { expect, test, type APIRequestContext } from "@playwright/test";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const PAGE = `/replay/${MANIFEST}`;
const CORE = "http://127.0.0.1:18080";
const TOKEN = "e2e-token";

/** The fixture core is shared and stateful: a mutating test first returns the cursor to a known position. */
async function releaseTo(request: APIRequestContext, episode: number) {
  const res = await request.post(`${CORE}/replay/manifests/${MANIFEST}/reset`, {
    headers: { authorization: `Bearer ${TOKEN}` },
    data: { episode },
  });
  if (!res.ok()) throw new Error(`fixture reset to ${episode} failed: ${res.status()}`);
}

test("the replay index opens a typed manifest", async ({ page }) => {
  await page.goto("/replay");
  await page.getByLabel("Demo manifest id").fill(MANIFEST);
  await page.getByRole("button", { name: "Open replay" }).click();
  await expect(page).toHaveURL(new RegExp(`${PAGE}$`));
  await expect(page.getByRole("heading", { level: 1, name: "Episode replay" })).toBeVisible();
});

test("the viewer shows released badges, the withheld card and an unnamed future slot", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);

  const rail = page.getByRole("region", { name: "Episodes" });
  const items = rail.getByRole("listitem");
  await expect(items).toHaveCount(5);
  await expect(items.nth(0)).toContainText("Before the first event");
  await expect(items.nth(1)).toContainText("Material");
  await expect(items.nth(2)).toContainText("No action required");
  await expect(items.nth(2)).toContainText("no_material_change");
  await expect(items.nth(2)).toContainText("coalesced fold");

  // The withheld next event: identity only, consequences visibly hidden.
  const withheld = page.getByTestId("withheld-card");
  await expect(withheld).toContainText("#3");
  await expect(withheld).toContainText("Withheld");
  await expect(withheld).toContainText("Consequences are hidden");
  await expect(withheld).not.toContainText("Material");
  await expect(withheld.getByRole("link")).toHaveCount(0);

  await expect(items.nth(4)).toContainText("Not yet reached (held-out window)");

  const detail = page.getByRole("region", { name: "Episode detail" });
  await expect(detail).toContainText("v7");
  await expect(detail).toContainText("diff #40");
  await expect(detail).toContainText("Security questionnaires stall without the SOC2 packet attached");
});

test("Play next releases one event and reports the verdict", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);
  await page.getByRole("button", { name: "Play next" }).click();
  await expect(page.getByRole("status").first()).toContainText("Released episode 3 of 4: material · state v8");

  const rail = page.getByRole("region", { name: "Episodes" });
  await expect(rail.getByRole("listitem").nth(3)).toContainText("Material");
  // Episode 4 — the held-out event — is now the withheld next event, still with hidden consequences.
  const withheld = page.getByTestId("withheld-card");
  await expect(withheld).toContainText("#4");
  await expect(withheld).toContainText("held-out");
});

test("the ?at navigator reviews an earlier released position read-only", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);
  const rail = page.getByRole("region", { name: "Episodes" });
  await rail.getByRole("link", { name: /#1/ }).click();
  await expect(page).toHaveURL(/at=1/);

  await expect(page.getByRole("status")).toContainText("Read-only view of the world as of episode 1");
  await expect(page.getByRole("button", { name: "Play next" })).toHaveCount(0);
  const detail = page.getByRole("region", { name: "Episode detail" });
  await expect(detail).toContainText("v6");
  // Position 2 is the withheld next event of this earlier boundary, even though it is released at the cursor.
  await expect(page.getByTestId("withheld-card")).toContainText("#2");

  await page.getByRole("link", { name: "Latest", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`${PAGE}$`));
  await expect(page.getByRole("button", { name: "Play next" })).toBeVisible();
});

test("?at at the released cursor is the live view: controls stay and no Forward is offered", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(`${PAGE}?at=2`);

  // The latest released boundary reached via ?at must behave like the live view (review fix):
  // Play next and Reset stay usable instead of the read-only banner hiding them.
  await expect(page.getByRole("button", { name: "Play next" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reset" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Forward ›" })).toHaveCount(0);

  // A middle position keeps Forward inside the released range.
  await page.goto(`${PAGE}?at=1`);
  await expect(page.getByRole("link", { name: "Forward ›" })).toBeVisible();
  await page.getByRole("link", { name: "Forward ›" }).click();
  await expect(page).toHaveURL(new RegExp(`${PAGE}$`));
});

test("Reset asks for confirmation and returns the cursor to the start", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto(PAGE);
  page.once("dialog", (d) => void d.accept());
  await page.getByRole("button", { name: "Reset" }).click();
  await expect(page.getByRole("status").first()).toContainText("reset to episode 0");

  const rail = page.getByRole("region", { name: "Episodes" });
  await expect(rail.getByRole("listitem").nth(1)).toContainText("Withheld");
  await expect(page.getByRole("region", { name: "Episode detail" })).toContainText("No event has been released");
});

test("an unknown manifest is a 404 page, not a crash", async ({ page }) => {
  const res = await page.goto("/replay/0d3a0000-0000-4000-8000-00000000dead");
  expect(res?.status()).toBe(404);
  await expect(page.getByRole("heading", { name: "Manifest not found" })).toBeVisible();
});

test("the runs index lists decision runs with their generation phase", async ({ page }) => {
  await page.goto("/runs");
  const table = page.getByRole("table");
  await expect(table).toContainText("post_interaction_followup");
  await expect(table).toContainText("awaiting_human");
  await expect(table).toContainText("published");
  await expect(table).toContainText("evaluating");
  await expect(table.getByRole("row")).toHaveCount(3); // header + 2 runs
});
