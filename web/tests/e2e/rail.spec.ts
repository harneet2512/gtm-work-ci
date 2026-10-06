import { expect, test, type APIRequestContext } from "@playwright/test";

const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const CORE = `http://127.0.0.1:${process.env.E2E_CORE_PORT ?? 18080}`;
const TOKEN = "e2e-token";

async function releaseTo(request: APIRequestContext, episode: number) {
  const res = await request.post(`${CORE}/replay/manifests/${MANIFEST}/reset`, { headers: { authorization: `Bearer ${TOKEN}` }, data: { episode } });
  if (!res.ok()) throw new Error(`fixture reset to ${episode} failed: ${res.status()}`);
}

test("the rail has exactly the four product areas", async ({ page }) => {
  await page.goto("/control");
  const labels = await page.getByRole("navigation", { name: "Sections" }).locator(".rail-label").evaluateAll((els) => els.map((e) => e.textContent));
  expect(labels).toEqual(["Intelligence", "Decision & Learning", "Cliff / Experience", "System"]);
});

test("Demo mode hides Replay so Play on Control is the only trigger", async ({ page }) => {
  await page.goto("/control");
  await expect(page.getByRole("navigation", { name: "Sections" }).getByRole("link", { name: "Replay" })).toBeVisible();
  await page.goto("/control?demo=1");
  await expect(page.getByRole("navigation", { name: "Sections" }).getByRole("link", { name: "Replay" })).toHaveCount(0);
});

test("Cliff messages says so when nothing has been played", async ({ page, request }) => {
  await releaseTo(request, 0);
  await page.goto("/cliff");
  await expect(page.getByText("No Cliff messages yet — press Play on Control")).toBeVisible();
});

test("Cliff messages lands on the latest played episode in Cliff mode", async ({ page, request }) => {
  await releaseTo(request, 2);
  await page.goto("/cliff");
  await expect(page).toHaveURL(/\/episodes\/[0-9a-f-]+\?mode=cliff/);
});
