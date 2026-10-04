import { expect, test } from "@playwright/test";

const ACCOUNT = "0a0c0000-0000-4000-8000-000000000001";
const EVENT = "05e00000-0000-4000-8000-000000000101";
const PAGE = `/accounts/${ACCOUNT}?event=${EVENT}`;

test("accounts list links to the account map", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("link", { name: "Acme Corp" }).click();
  await expect(page).toHaveURL(new RegExp(`/accounts/${ACCOUNT}$`));
  await expect(page.getByRole("heading", { level: 1, name: "Acme Corp" })).toBeVisible();
});

test("Before Play shows the N-1 map and timeline, the confirmed state, and no highlights", async ({ page }) => {
  await page.goto(PAGE);

  await expect(page.getByRole("heading", { name: "Before Play: account map" })).toBeVisible();
  const map = page.getByTestId("account-map");
  await expect(map.getByRole("button", { name: "Account: Acme Corp" })).toBeVisible();
  await expect(map.getByRole("button", { name: "Person: Priya Shah" })).toBeVisible();
  await expect(map.getByRole("button", { name: "Opportunity: Acme EU expansion" })).toBeVisible();
  await expect(map.getByRole("button", { name: /^INVOLVES: / })).toHaveCount(1);
  await expect(map.getByRole("button", { name: /Activity: EmailReceived/ })).toHaveCount(0);
  await expect(map.locator("[data-mark]")).toHaveCount(0);

  const timeline = page.getByRole("region", { name: "Timeline" });
  await expect(timeline.getByText(/History through 2026-09-29 15:42 UTC/)).toBeVisible();
  await expect(timeline.getByRole("listitem")).toHaveCount(2);
  await expect(timeline.getByText("EmailReceived")).toHaveCount(0);

  // H1/H2: the world read is strictly before N, so none of N's values leak into the Before view.
  await expect(page.getByText("at_risk")).toHaveCount(0);
  await expect(page.getByText(/SOC2/)).toHaveCount(0);

  await expect(page.getByTestId("transition-badge")).toHaveAttribute("data-kind", "CONFIRMED");
});

test("clicking a node, a claim and a timeline item opens the evidence it came from", async ({ page }) => {
  await page.goto(`${PAGE}&view=after`);
  const provenance = page.getByRole("region", { name: "Provenance" });
  await expect(provenance.getByText(/Click a node/)).toBeVisible();

  await page.getByTestId("account-map").getByRole("button", { name: /^Claim: health/ }).click();
  await expect(provenance.getByRole("heading", { name: "Claim: health" })).toBeVisible();
  await expect(provenance.getByText(/requires SOC2 Type II \+ pen-test summary/)).toBeVisible();

  await page.getByRole("button", { name: "Claim health" }).click();
  await expect(provenance.getByText(/we'll need your SOC2 Type II report and the pen-test summary/)).toBeVisible();
  await expect(provenance.getByText("first_party_ai", { exact: false })).toBeVisible();

  await page.getByRole("region", { name: "Timeline" }).getByRole("button", { name: /MeetingCompleted/ }).click();
  await expect(provenance.getByRole("heading", { name: "Activity: MeetingCompleted" })).toBeVisible();
});

test("After Play highlights exactly what the event changed and flips the badge to CANDIDATE", async ({ page }) => {
  await page.goto(PAGE);
  await page.getByRole("link", { name: "After Play" }).click();
  await expect(page).toHaveURL(/view=after/);

  await expect(page.getByRole("heading", { name: "After Play: what changed" })).toBeVisible();
  const map = page.getByTestId("account-map");
  await expect(map.locator('[data-node-id][data-mark="added"]')).toHaveCount(3);
  await expect(map.locator('[data-node-id][data-mark="changed"]')).toHaveCount(1);
  // Only the changes attributed to the event are highlighted (M2): the coalesced tech-evaluator edge is not.
  await expect(map.locator('[data-edge-id][data-mark="added"]')).toHaveCount(6);
  await expect(map.getByRole("button", { name: "Activity: EmailReceived, added" })).toBeVisible();

  const diff = page.getByTestId("diff-summary");
  await expect(diff.getByText("10 added")).toBeVisible();
  await expect(diff.getByText(/health: unknown -> at_risk/)).toBeVisible();
  await expect(diff.getByText(/INFLUENCES: Marco Ruiz -> Acme EU expansion/)).toBeVisible();

  await expect(page.getByRole("region", { name: "Timeline" }).getByText("from this event")).toBeVisible();

  const badge = page.getByTestId("transition-badge");
  await expect(badge).toHaveAttribute("data-kind", "CANDIDATE");
  await expect(badge).toContainText("REORG -> EXPANSION");
});

test("the browser never receives the core token", async ({ page }) => {
  const bodies: string[] = [];
  page.on("response", async (r) => {
    if (r.url().includes("127.0.0.1:3100")) bodies.push(await r.text().catch(() => ""));
  });
  await page.goto(`${PAGE}&view=after`);
  await expect(page.getByTestId("account-map")).toBeVisible();
  expect(bodies.join("\n")).not.toContain("e2e-token");
});

test("an unknown account is a 404 page, not a crash", async ({ page }) => {
  const res = await page.goto("/accounts/0a0c0000-0000-4000-8000-0000000000ff");
  expect(res?.status()).toBe(404);
  await expect(page.getByRole("heading", { name: "Account not found" })).toBeVisible();
});
