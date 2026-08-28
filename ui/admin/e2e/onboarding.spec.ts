import { expect, test } from "@playwright/test";

test("an empty backend reaches browser setup and persists the administrator session", async ({ page }) => {
  const setupToken = process.env.PGBASE_SETUP_TOKEN;
  if (!setupToken) throw new Error("PGBASE_SETUP_TOKEN is required");

  await page.goto("/admin/");
  await expect(page.getByRole("heading", { name: "Set up pgBase" })).toBeVisible();
  await page.getByLabel("Setup token").fill(setupToken);
  await page.getByLabel("Email").fill("admin@example.test");
  await page.getByLabel("Password", { exact: true }).fill("correct horse battery staple 2026!");
  await page.getByLabel("Confirm password").fill("correct horse battery staple 2026!");
  await page.getByRole("button", { name: "Create administrator" }).click();

  await expect(page.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
  await expect(page.getByText("admin@example.test")).toBeVisible();
});
