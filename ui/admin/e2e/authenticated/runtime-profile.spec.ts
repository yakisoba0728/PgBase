import { expect, test } from "./fixtures";

test("persists the administrator profile, network policy and runtime log settings", async ({ page }) => {
  await page.goto("/admin/settings");

  await expect(page.getByRole("heading", { name: "Application settings" })).toBeVisible();
  const profile = page.getByRole("region", { name: "Administrator profile" });
  await expect(profile).toBeVisible();

  await profile.getByLabel("Email").fill("settings-admin@pgbase.test");
  await profile.getByRole("button", { name: "Save administrator email" }).click();
  await expect(profile.getByText("Administrator email updated.")).toBeVisible();

  await profile.getByLabel("Allowed CIDRs").fill("127.0.0.1/32");
  await profile.getByRole("button", { name: "Save IP allowlist" }).click();
  await expect(profile.getByText("IP allowlist updated.")).toBeVisible();

  await page.getByLabel("Level").selectOption("warn");
  await page.getByLabel("Include IP addresses").check();
  await page.getByLabel("Include authentication events").check();
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();

  await page.reload();
  await expect(page.getByLabel("Level")).toHaveValue("warn");
  await expect(page.getByLabel("Include IP addresses")).toBeChecked();
  await expect(page.getByLabel("Include authentication events")).toBeChecked();
});
