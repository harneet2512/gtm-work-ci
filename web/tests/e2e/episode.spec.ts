import { expect, test } from "@playwright/test";

// The MedTech decision episode (live metrics, an edited draft, no knowledge cited), the Acme episode (knowledge changed,
// including a DEMOTE) and the episode the replay's Play event 3 opens (Message 1 tied to its own account change).
const EPISODE = "0e9ead00-0000-4000-8000-00000000ad0a";
const ACME_EPISODE = "0e9e0000-0000-4000-8000-000000000a01";
const REPLAY_EPISODE = "0de50000-0000-4000-8000-000000000202";
const PAGE = `/episodes/${EPISODE}`;
const node = (page: import("@playwright/test").Page, id: string) => page.locator(`[id="node-${id}"]`);

test("the episode page renders the served trace with honest statuses", async ({ page }) => {
  await page.goto(PAGE);

  await expect(page.getByRole("heading", { level: 1 })).toContainText("0e9ead00");
  await expect(page.locator(".context-bar")).toContainText("MedTech Advances");
  const rail = page.getByRole("list", { name: "Causal trajectory" });
  await expect(rail).toContainText("Source event");
  await expect(rail).toContainText("Fatoumata");
  await expect(rail).toContainText("6 evidence references behind the account change");
  await expect(rail).toContainText("State v107 to v108");
  await expect(rail).toContainText("3 candidates in the strategy set");
  await expect(rail).toContainText("Ghost preferred Book the call"); // core-served span text (follow-up: core copy rename)
  // Precedents are never persisted: the span says so instead of inventing any.
  await expect(node(page, "precedents:0")).toContainText("Precedent retrieval is not recorded yet");
  await expect(node(page, "precedents:0").locator(".sr-only")).toHaveText("not recorded");
  // MedTech cited no knowledge: all three knowledge spans read not recorded, and Influence stays "not measured".
  for (const kind of ["knowledge_retrieved", "knowledge_applicable", "knowledge_used"]) await expect(node(page, `${kind}:0`).locator(".sr-only")).toHaveText("not recorded");
  await expect(node(page, "knowledge_influence")).toContainText("Influence: not measured");
  await expect(node(page, "knowledge_influence").locator(".sr-only")).toHaveText("not measured");
  // Only Message 2 is posted for this episode; the rail reports what is on record and no more.
  await expect(node(page, "cliff_message:chooser")).toContainText("posted");
  await expect(node(page, "cliff_message:bi").locator(".sr-only")).toHaveText("not recorded");
  await expect(node(page, "recomputed_action:0d5dad00-0000-4000-8000-00000000ad70")).toContainText("re-evaluated at send time");
});

test("story mode shows what the episode did to knowledge and what the edit changed", async ({ page }) => {
  await page.goto(PAGE);
  await expect(page.getByRole("heading", { name: "Knowledge mutations" })).toBeVisible();
  await expect(page.getByText("This episode changed no company knowledge.")).toBeVisible();
  const edit = page.getByRole("region", { name: "What the edit changed" });
  await expect(edit).toContainText("re-evaluated at send time");
  await expect(edit).toContainText("Account state preserved");
  for (const name of ["Invalidated", "Re-evaluated", "Not recomputed", "Preserved"]) await expect(edit.getByRole("heading", { name }).first()).toBeVisible();
});

test("the Acme episode lists its knowledge mutations: operation (DEMOTE included), scope and the version at that time", async ({ page }) => {
  await page.goto(`/episodes/${ACME_EPISODE}`);
  const panel = page.getByRole("region", { name: "Knowledge mutations", exact: true });
  await expect(panel.getByRole("row")).toHaveCount(3); // header + SUPPORT + DEMOTE
  await expect(panel).toContainText("SUPPORT");
  const demote = panel.getByRole("row").filter({ hasText: "DEMOTE" });
  await expect(demote).toContainText("reusable candidate");
  await expect(demote).toContainText("v3 as of 2026-09-29T16:12:00Z");
  await expect(demote).toContainText("supported → provisional");
  await expect(demote.getByRole("link").first()).toHaveAttribute("href", /\/knowledge\//);
  // The three knowledge steps are separate nodes, and cited is a citation, not influence.
  await expect(node(page, "knowledge_retrieved:0")).toContainText("retrieved");
  await expect(node(page, "knowledge_used:0")).toContainText("cited by a candidate");
  await expect(node(page, "knowledge_influence")).toContainText("not measured");
});

test("trace mode shows the span table", async ({ page }) => {
  await page.goto(`${PAGE}?mode=trace`);
  const table = page.getByRole("table").first();
  await expect(table.getByRole("row")).toHaveCount(1 + 17);
  await expect(table).toContainText("Source event");
  await expect(table.getByRole("row").filter({ hasText: "Precedents" })).toContainText("not recorded");
  await expect(table.getByRole("row").filter({ hasText: "Cliff message: judgment" })).toContainText("recorded");
  await expect(table.getByRole("row").filter({ hasText: "Candidates" })).toContainText("strategy set");
});

test("graph diff mode shows the event's projection changes and the state move", async ({ page }) => {
  await page.goto(`${PAGE}?mode=graphdiff`);
  // The MedTech fixture projects the neighborhood as the core does: 26 added, the superseded use case changed.
  await expect(page.getByText(/projection diff · \+26 −0 ~1/)).toBeVisible();
  await expect(page.getByRole("table").first()).toContainText("Conversation");
  await expect(page.getByText("account state v107 → v108")).toBeVisible();
  await expect(page.getByRole("list", { name: "State fields that changed" })).toContainText("objections");
});

test("raw mode exposes the payloads and the inspector shows the selected node", async ({ page }) => {
  await page.goto(`${PAGE}?mode=raw&node=${encodeURIComponent("knowledge_mutation:0")}`);
  await expect(page.locator(".raw")).toBeVisible();
  await expect(page.locator(".inspector")).toContainText("Knowledge mutation");
  await expect(page.locator(".inspector")).toContainText("not recorded");
  await expect(page.locator(".inspector")).toContainText("This episode changed no company knowledge");
});

test("cliff mode shows each message beside its posting receipt", async ({ page }) => {
  await page.goto(`${PAGE}?mode=cliff`);
  await expect(page.getByText("Message 2 — strategy chooser")).toBeVisible();
  const m2 = page.locator(".msg-row").nth(1);
  await expect(m2).toContainText("posted");
  await expect(m2.locator(".slack-card")).toContainText("gtm_ai drafted 3 moves");
  await expect(m2.locator(".slack-card")).toContainText("chosen");
  // MedTech has no BI update on its account: Message 1 says so, honestly.
  await expect(page.locator(".msg-row").first()).toContainText("No business-intelligence update on this account");
  // M3 is reserved but unposted: the write-once ts is still null.
  await expect(page.locator(".msg-row").nth(2)).toContainText("ts pending");
});

test("Message 1 is tied to this episode's own account change, not the account's latest", async ({ page }) => {
  await page.goto(`/episodes/${REPLAY_EPISODE}?mode=cliff`);
  const m1 = page.locator(".msg-row").first();
  await expect(m1).toContainText("posted");
  await expect(m1.locator(".slack-card")).toBeVisible();
  // Another episode of the same account must not borrow that update.
  await page.goto(`/episodes/${ACME_EPISODE}?mode=cliff`);
  await expect(page.locator(".msg-row").first()).toContainText("not resolvable");
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

test("a deep link /episodes/:id?node=... lands on that node: inspector shows it and the rail link has focus", async ({ page }) => {
  await page.goto(`${PAGE}?node=${encodeURIComponent("cliff_message:chooser")}`);
  await expect(page.locator(".inspector")).toContainText("Cliff message: chooser");
  await expect(page.locator('[id="node-cliff_message:chooser"] a')).toBeFocused();
  await expect(node(page, "cliff_message:chooser")).toHaveClass(/selected/);
});

test("a deep link naming an unknown node says so and still renders the trajectory", async ({ page }) => {
  await page.goto(`${PAGE}?node=nope`);
  await expect(page.getByText("No node “nope” on this trajectory")).toBeVisible();
  await expect(page.getByRole("list", { name: "Causal trajectory" })).toContainText("Source event");
});
