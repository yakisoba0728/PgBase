import { expect, test } from "./fixtures";

test("creates and activates a managed backup restore", async ({ page }) => {
  test.skip(process.env.PGBASE_BROWSER_MANAGED_BACKUPS !== "1", "requires a disposable managed-host fixture");
  test.setTimeout(120_000);
  await page.goto("/admin/system");

  const created = page.waitForResponse((response) => response.url().endsWith("/api/admin/backups") && response.request().method() === "POST");
  await page.getByRole("button", { name: "Create backup", exact: true }).click();
  const job = await (await created).json() as { backupId: string };
  await expect(page.getByText(new RegExp(`create backup ${job.backupId}: completed`))).toBeVisible({ timeout: 60_000 });

  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Restore", exact: true }).click();
  await expect(page.getByText("Restore started.")).toBeVisible();
  await expect(page.getByText(new RegExp(`restore backup ${job.backupId}: completed`))).toBeVisible({ timeout: 90_000 });
  await expect(page.getByRole("heading", { name: "System" })).toBeVisible();
});
