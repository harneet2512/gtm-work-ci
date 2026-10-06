import { expect, test } from "@playwright/test";

// The demo's second Play: the account on screen has no episode left, so Play continues into the next account through the
// hidden handoff. While core switches to it every replay read fails, and the strip must never call that "backend
// unavailable": nothing is wrong, the demo is moving on. (The stand-in control service is fixture-demo-control.mjs.)
const MANIFEST = "0d3a0000-0000-4000-8000-000000000501";
const NEXT = "0d3a0000-0000-4000-8000-000000000502";
const CONTROL = `http://127.0.0.1:${process.env.E2E_CONTROL_PORT ?? 18081}`;

test.afterEach(async ({ request }) => {
  await request.post(`${CONTROL}/__reset`);
});

test("a second Play goes through the handoff and the strip never says unavailable", async ({ page, request }) => {
  await request.post(`${CONTROL}/__arm`);
  await request.post(`${CONTROL}/__release_all`);
  // Record every moment the page shows a false word, not just the final state. "unavailable" is never true here; "complete" and
  // "passed" are MedTech old result, which must never read as the result of the new Play (counted from the press until Play returns).
  await page.addInitScript((next) => {
    const w = window as unknown as { __seen: string[]; __pressed: boolean };
    w.__seen = [];
    w.__pressed = false;
    new MutationObserver(() => {
      if (location.href.includes(next)) return; // the next account is on screen: its own finished pipeline is true there
      const strip = (document.querySelector(".pipeline") as HTMLElement | null)?.innerText ?? ""; // the Play strip, not the account page around it
      if (/event \d+ of \d+/i.test(strip)) w.__pressed = false; // Play has returned and named the next account: the strip now follows that one
      if (/unavailable/i.test(document.body?.innerText ?? "") || (w.__pressed && /pipeline complete|passed|completed/i.test(strip))) w.__seen.push(location.href + " @" + Math.round(performance.now()) + " :: " + (strip || "(page)").replace(/\n/g, " ").slice(0, 400));
    }).observe(document, { subtree: true, childList: true, characterData: true });
  }, NEXT);
  await page.goto(`/control?manifest=${MANIFEST}&demo=1`);
  await expect(page.locator(".pipeline-head")).toContainText("The next episode is withheld until release.");
  await expect(page.getByRole("button", { name: "Play event N" })).toBeEnabled();

  await page.getByRole("button", { name: "Play event N" }).click();
  await page.evaluate(() => ((window as unknown as { __pressed: boolean }).__pressed = true)); // from here on the old result is stale
  await expect(page).toHaveURL(new RegExp(`manifest=${NEXT}`), { timeout: 30000 });
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __seen?: string[] }).__seen ?? [])).toEqual([]);
});
