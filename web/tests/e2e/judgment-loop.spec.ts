import { expect, test, type Page } from "@playwright/test";
import path from "node:path";

// HAR-145: the Judgment Episode hero, the episode's protection view in causal order, System Proof, the demo explainer and the
// filters. Screenshots are written only when HAR145_SHOTS_DIR names a folder.
const EPISODE = "0e9ead00-0000-4000-8000-00000000ad0a";
const DIR = process.env.HAR145_SHOTS_DIR ?? "";
test.use({ viewport: { width: 1600, height: 1000 } });

const shot = async (page: Page, name: string) => {
  if (DIR) await page.screenshot({ path: path.join(DIR, `har145-${name}.png`), fullPage: false });
};
const watchErrors = (page: Page) => {
  const errors: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  return errors;
};

test("the episode page opens on the Judgment Episode hero, one block per Cliff message", async ({ page }) => {
  const errors = watchErrors(page);
  await page.goto(`/episodes/${EPISODE}`);
  const hero = page.locator('[data-view="judgment-hero"]');
  await expect(hero.getByRole("heading", { level: 2, name: "Judgment Episode" })).toBeVisible();
  await expect(hero.locator("[data-message]")).toHaveCount(3);
  await expect(hero.locator('[data-message="M1"]')).toContainText("Message 1 — what changed");
  await expect(hero.locator('[data-message="M2"]')).toContainText("gtm_ai recommendation");
  await expect(hero.locator('[data-message="M2"]')).toContainText("Alternatives:");
  await expect(hero.locator('[data-message="M2"]')).toContainText("Human Judgment");
  await expect(hero.locator('[data-message="M3"]')).toContainText("Eval Gap");
  await expect(hero.locator('[data-message="M3"]')).toContainText("Judgment Learning");
  await expect(hero.getByRole("link", { name: "Slack rendering of M2" })).toHaveAttribute("href", /mode=cliff/);
  await expect(hero.getByRole("link", { name: "Evals of M3" })).toHaveAttribute("href", /view=cards.*#cards-M3/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(/^MedTech Advances · Email from Fatoumata · Nov 9, 2023$/);
  const delta = hero.getByTestId("judgment-delta");
  await expect(delta.locator(".delta-chip")).toBeVisible();
  await expect(delta).not.toContainText("unclassified ·");
  const shown = (await delta.locator(".delta-diff").first().innerText()).length;
  expect(shown).toBeLessThanOrEqual(240);
  await delta.getByRole("button", { name: "Show full edit" }).click();
  await expect(delta.locator(".delta-full")).toContainText("Before");
  const labels = await page.locator(".rail-label").allInnerTexts();
  expect(labels.map((l) => l.trim())).toEqual(["INTELLIGENCE · M1", "JUDGMENT LOOP · M2–M3", "CLIFF MESSAGES · M1–M3", "SYSTEM TRUST"].map((l) => expect.stringMatching(new RegExp(`^${l}$`, "i")) as unknown as string));
  await expect(hero).not.toContainText("Ghost");
  await expect(hero).not.toContainText(/learned/i); // a candidate or shadow rule is never "learned"
  await expect(page.getByTestId("presenter-hint")).toHaveCount(0); // Demo mode only
  await shot(page, "01-judgment-episode-hero");
  expect(errors).toEqual([]);
});

test("Demo mode shows the presenter hint, and it can be dismissed", async ({ page }) => {
  await page.goto(`/episodes/${EPISODE}?demo=1`);
  const hint = page.getByTestId("presenter-hint");
  await expect(hint).toBeVisible();
  await shot(page, "02-presenter-hint-demo");
  await hint.getByRole("button", { name: /Dismiss/ }).click();
  await expect(hint).toHaveCount(0);
});

test("What protected this decision? lists live and conditional gates in causal order", async ({ page }) => {
  await page.goto(`/episodes/${EPISODE}`);
  const view = page.locator('[data-view="episode-protection"]');
  await expect(view.getByRole("heading", { name: "What protected this decision?" })).toBeVisible();
  await expect(view.locator("[data-phase]")).toHaveText([/INGEST/, /DECIDE/, /HUMAN/, /SEND/]);
  const ids = await view.locator("li[data-gate]").evaluateAll((els) => els.map((e) => e.getAttribute("data-gate")));
  expect(ids).toEqual(["B1", "B2", "B3", "B4", "B7", "B8", "D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"]);
  await expect(view.locator('li[data-gate="D5"]')).toHaveAttribute("data-conditional", "true");
  await expect(view.locator('li[data-gate="D5"]')).toContainText("D5?");
  await expect(view.locator('li[data-gate="B1"]')).toHaveAttribute("data-conditional", "false");
  await expect(view).not.toContainText("S2"); // offline gates belong to System Proof
  await expect(view.locator('li[data-gate="D5"], li[data-gate="D6"], li[data-gate="D10"]').first()).toBeVisible();
  await shot(page, "03-what-protected-this-decision");
});

test("System Proof holds S2-S5 and offline coverage, with calibration as its own row", async ({ page }) => {
  const errors = watchErrors(page);
  await page.goto("/evals?view=proof");
  const proof = page.locator('[data-view="system-proof"]');
  await expect(page.getByRole("heading", { level: 1 })).toContainText("How do we know the eval system is trustworthy?");
  await expect(proof.locator("#proof-S2")).toContainText("Eval Trust");
  for (const id of ["S2", "S3", "S4", "S5"]) await expect(proof.locator(`#proof-${id}`)).toBeVisible();
  await expect(proof.locator("tbody tr")).toHaveCount(19);
  await expect(proof.locator('tbody tr[data-gate="D8"]')).toContainText("not measured");
  await expect(proof).toContainText("Calibration: not yet calibrated");
  await expect(proof).not.toContainText(/\bPASS\b/); // never a live verdict here
  await shot(page, "04-system-proof");
  expect(errors).toEqual([]);
});

test("in Demo mode the mode badge explains the gate in three lines", async ({ page }) => {
  await page.goto("/evals?view=cards&demo=1");
  const d8 = page.locator("#card-D8");
  await d8.getByRole("button", { name: "Live" }).hover();
  const pop = d8.getByRole("tooltip");
  await expect(pop).toContainText("What it is");
  await expect(pop).toContainText("When it runs");
  await expect(pop).toContainText("Live · immediately before Send");
  await expect(pop).toContainText("What failure changes");
  await expect(pop).toContainText("blocks current action");
  await shot(page, "05-demo-explainer-d8");
  const s3 = page.locator("#card-S3");
  await s3.getByRole("button", { name: "Offline" }).click();
  await expect(s3.getByRole("tooltip")).toContainText("monitoring only");
});

test("the Gates view groups by message and the table filters by status, mode and impact", async ({ page }) => {
  const errors = watchErrors(page);
  await page.goto("/evals?view=cards");
  await expect(page.locator("section.message-section h3")).toHaveText(["Message 1 · What changed", "Message 2 · What to do", "Message 3 · What we learned from you", "EcoLite Play · Used next time", "System Trust"]);
  await shot(page, "06-gate-cards-by-message");

  await page.goto("/evals");
  await page.getByRole("button", { name: /NOT RUN$/ }).click();
  for (const r of await page.locator("tbody tr[data-gate]").all()) await expect(r).toContainText("NOT RUN");
  await page.getByRole("button", { name: "blocks current action" }).click();
  const gates = await page.locator("tbody tr[data-gate]").evaluateAll((els) => [...new Set(els.map((e) => e.getAttribute("data-gate")))]);
  expect(gates).toEqual(["D8"]);
  await page.getByRole("button", { name: "blocks current action" }).click();
  await page.getByRole("button", { name: "Conditional" }).click();
  const cond = await page.locator("tbody tr[data-gate]").evaluateAll((els) => els.map((e) => e.getAttribute("data-gate")));
  expect(cond.every((g) => ["B5", "B6", "B7", "B9", "D4", "D5", "D6", "D7", "D10"].includes(g!))).toBe(true);
  await shot(page, "07-gate-results-filters");
  expect(errors).toEqual([]);
});
