import { expect, test } from "@playwright/test";

// The MedTech decision episode — the run fixture's generation.decision_episode_id.
const EPISODE = "0e9ead00-0000-4000-8000-00000000ad0a";
const PAGE = `/episodes/${EPISODE}`;

test("the episode page renders the causal chain with honest statuses", async ({ page }) => {
  await page.goto(PAGE);

  await expect(page.getByRole("heading", { level: 1 })).toContainText("0e9ead00");
  const rail = page.getByRole("list", { name: "Causal trajectory" });
  await expect(rail).toContainText("Source event");
  await expect(rail).toContainText("Fatoumata");
  await expect(rail).toContainText("6 correlated activities");
  await expect(rail).toContainText("v107 → v108");
  await expect(rail).toContainText("3 candidates");
  await expect(rail).toContainText("Book the call");
  // MedTech cited no knowledge — the node says so instead of fabricating a retrieval.
  await expect(rail).toContainText("No company-knowledge read is recorded");
  await expect(rail).toContainText("Influence: not measured");
  // The fixture posts only Message 2 for this episode — the rail reports what is posted, no more.
  await expect(rail).toContainText("Message 2 posted to Slack");
  await expect(rail).toContainText("chose edit");
  await expect(rail).toContainText("overrode");
});

test("trace mode shows the span table", async ({ page }) => {
  await page.goto(`${PAGE}?mode=trace`);
  await expect(page.getByRole("table").first()).toContainText("trigger");
  await expect(page.getByRole("table").first()).toContainText("evidence");
  const accesses = page.getByRole("table").nth(1);
  await expect(accesses).toContainText("state");
  await expect(accesses).toContainText("evidence");
});

test("graph diff mode shows the event's projection changes and the state diff", async ({ page }) => {
  await page.goto(`${PAGE}?mode=graphdiff`);
  await expect(page.getByText(/projection diff · \+10 −0 ~0/)).toBeVisible();
  await expect(page.getByRole("table").first()).toContainText("Activity");
  await expect(page.getByText("State field")).toBeVisible();
});

test("raw mode exposes the payloads and the inspector shows the selected node", async ({ page }) => {
  await page.goto(`${PAGE}?mode=raw&node=knowledge_mutation`);
  await expect(page.getByRole("group").or(page.locator(".raw")).first()).toBeVisible();
  await expect(page.locator(".inspector")).toContainText("Knowledge mutation");
  await expect(page.locator(".inspector")).toContainText("overrode");
});

test("cliff mode shows each message beside its posting receipt", async ({ page }) => {
  await page.goto(`${PAGE}?mode=cliff`);
  await expect(page.getByText("Message 2 — strategy chooser")).toBeVisible();
  // M2 posted; the card lists the three options with Ghost's pick and the human's choice.
  const m2 = page.locator(".msg-row").nth(1);
  await expect(m2).toContainText("posted");
  await expect(m2.locator(".slack-card")).toContainText("Ghost drafted 3 moves");
  await expect(m2.locator(".slack-card")).toContainText("chosen");
  // M1 has no BI update on the medtech account — the row says so, honestly.
  await expect(page.locator(".msg-row").first()).toContainText("Not applicable");
  // Without the manifest the episode's account change is unknown, so Message 1 is never guessed from "latest".
  await expect(page.locator(".msg-row").first()).toContainText("not resolvable");
  // M3 is reserved but unposted — the write-once ts is still null.
  await expect(page.locator(".msg-row").nth(2)).toContainText("ts pending");
});

test("a run page links to its decision episode", async ({ page }) => {
  await page.goto("/runs/0f0aad00-0000-4000-8000-00000000ad60");
  await page.getByRole("link", { name: "Episode" }).click();
  await expect(page).toHaveURL(new RegExp(`/episodes/${EPISODE}$`));
});

test("an unknown episode 404s", async ({ page }) => {
  const res = await page.goto("/episodes/0e9ead00-0000-4000-8000-000000000000");
  expect(res?.status()).toBe(404);
});

test("a deep link /episodes/:id?node=cliff lands on that node: inspector shows it and the rail link has focus", async ({ page }) => {
  await page.goto(`${PAGE}?node=cliff`);
  await expect(page.locator(".inspector")).toContainText("Cliff");
  await expect(page.locator("#node-cliff a")).toBeFocused();
  await expect(page.locator("#node-cliff")).toHaveClass(/selected/);
});

test("a deep link naming an unknown node says so and still renders the trajectory", async ({ page }) => {
  await page.goto(`${PAGE}?node=nope`);
  await expect(page.getByText("No node “nope” on this trajectory")).toBeVisible();
  await expect(page.getByRole("list", { name: "Causal trajectory" })).toContainText("Source event");
});
