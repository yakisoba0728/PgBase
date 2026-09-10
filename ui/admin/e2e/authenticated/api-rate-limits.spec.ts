import { expect, test } from "./fixtures";

test("persists a guest API rule and limits only a separate guest client", async ({ page }) => {
  await page.goto("/admin/settings");
  await expect(page.getByRole("heading", { name: "Application settings" })).toBeVisible();
  await page.getByRole("checkbox", { name: "Enable API request rate limits" }).check();
  await page.getByRole("button", { name: "Add API rate rule" }).click();
  await page.getByLabel("Rule 1 label").fill("/health/live");
  await page.getByLabel("Rule 1 audience").selectOption("@guest");
  await page.getByLabel("Rule 1 duration seconds").fill("60");
  await page.getByLabel("Rule 1 maximum requests").fill("1");
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  await page.reload();
  await expect(page.getByRole("checkbox", { name: "Enable API request rate limits" })).toBeChecked();
  await expect(page.getByLabel("Rule 1 label")).toHaveValue("/health/live");

  const browser = page.context().browser();
  if (!browser) throw new Error("browser context is unavailable");
  const guest = await browser.newContext();
  try {
    const origin = new URL(page.url()).origin;
    expect((await guest.request.get(`${origin}/health/live`)).status()).toBe(200);
    expect((await guest.request.get(`${origin}/health/live`)).status()).toBe(429);
  } finally { await guest.close(); }

  await page.goto("/admin/system");
  await expect(page.getByRole("heading", { name: "System" })).toBeVisible();
});
