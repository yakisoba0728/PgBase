import type { Page } from "@playwright/test";
import { createCollection, deleteCollection, expect, signIn, test, uniqueName } from "./fixtures";

test("records a guarded apply in the redacted operation history", async ({ page }, info) => {
  const name = uniqueName("ops", info);
  await createCollection(page, name, [{ name: "title" }]);

  // The address a new tab or "copy link address" would use has to be the same
  // typed route an in-place click reaches.
  const link = page.getByRole("link", { name: "Schema operations" });
  await expect(link).toHaveAttribute("href", "/admin/schema-operations");
  await link.click();

  await expect(page.getByRole("heading", { name: "Schema operations" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/admin/schema-operations");

  // The apply this run just made is owned by this administrator, so it is on
  // the first bounded page the ledger returns.
  const rows = page.locator("ol.operation-list > li");
  await expect(rows.first()).toBeVisible();
  await expect(rows).not.toHaveCount(0);
  await expect(page.getByText("Create collection").first()).toBeVisible();

  // A page is bounded whatever the ledger holds: the UI cannot ask for more.
  expect(await rows.count()).toBeLessThanOrEqual(20);

  // The receipt is redacted server-side; the page must never render the SQL,
  // the request hash or the idempotency key even if one arrived.
  const rendered = await page.locator("body").innerText();
  expect(rendered).not.toMatch(/CREATE TABLE|ALTER TABLE|DROP TABLE/i);
  expect(rendered.toLowerCase()).not.toContain("idempotency");
  expect(rendered.toLowerCase()).not.toContain("requesthash");
  expect(rendered).not.toContain(name);

  await deleteCollection(page, name);
});

test("recovers an operation status by id after a reload and a fresh sign-in", async ({ browser, page }, info) => {
  const name = uniqueName("recov", info);
  await createCollection(page, name, [{ name: "title" }]);

  await page.goto("/admin/schema-operations");
  const first = page.locator("ol.operation-list > li").first();
  await expect(first).toBeVisible();
  // Other workers apply against the same administrator and one collection has
  // several operations, so the row is followed by the operation's own identity
  // rather than by its position or its collection.
  const operationID = (await first.locator(".operation-id").innerText()).trim();
  const status = (await first.locator(".operation-status").innerText()).trim();
  const row = (target: Page) => target.locator("ol.operation-list > li").filter({ hasText: operationID });

  // A reload keeps nothing client-side: the status is read back from the
  // owner-scoped ledger, so it survives the page and the session alike.
  await page.reload();
  await expect(row(page).locator(".operation-status")).toHaveText(status);

  const email = process.env.PGBASE_ADMIN_EMAIL ?? "";
  const password = process.env.PGBASE_ADMIN_PASSWORD ?? "";
  const baseURL = info.project.use.baseURL as string;
  const fresh = await browser.newPage({ storageState: { cookies: [], origins: [] } });
  try {
    await signIn(fresh, new URL("/admin/", baseURL).href, email, password);
    await fresh.goto("/admin/schema-operations");
    await expect(row(fresh).locator(".operation-status")).toHaveText(status);
  } finally {
    await fresh.close();
  }

  await deleteCollection(page, name);
});
