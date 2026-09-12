import { expect, signIn, test } from "./fixtures";

test("persists the administrator profile, network policy and runtime log settings", async ({ page, baseURL }) => {
  const originalEmail = process.env.PGBASE_ADMIN_EMAIL;
  const password = process.env.PGBASE_ADMIN_PASSWORD;
  if (!originalEmail || !password || baseURL === undefined) throw new Error("administrator credentials and a baseURL are required");
  const changedEmail = "settings-admin@pgbase.test";
  const adminURL = new URL("/admin/", baseURL).href;
  await page.goto("/admin/settings");

  await expect(page.getByRole("heading", { name: "Application settings" })).toBeVisible();
  const profile = page.getByRole("region", { name: "Administrator profile" });
  await expect(profile).toBeVisible();
  const originalCIDRs = await profile.getByLabel("Allowed CIDRs").inputValue();

  await page.getByLabel("Level").selectOption("warn");
  await page.getByLabel("Include IP addresses").check();
  await page.getByLabel("Include authentication events").check();
  await page.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible();

  await page.reload();
  await expect(page.getByLabel("Level")).toHaveValue("warn");
  await expect(page.getByLabel("Include IP addresses")).toBeChecked();
  await expect(page.getByLabel("Include authentication events")).toBeChecked();

  let policyChanged = false;
  let emailChanged = false;
  try {
    // These ranges exist only on the disposable browser fixture. They let the
    // test prove policy persistence without excluding its loopback or Compose
    // bridge address before the fixture can restore its original value.
    const policySaved = page.waitForResponse(response => response.url().endsWith("/api/admin/profile/ip-allowlist") && response.request().method() === "PATCH");
    const fixtureCIDRs = "172.16.0.0/12\n127.0.0.1/32";
    await profile.getByLabel("Allowed CIDRs").fill(fixtureCIDRs);
    await profile.getByRole("button", { name: "Save IP allowlist" }).click();
    policyChanged = (await policySaved).ok();
    expect(policyChanged).toBe(true);
    await expect(profile.getByText("IP allowlist updated.")).toBeVisible();
    // Reauthenticate from an empty browser session to prove the saved policy
    // still admits this fixture's network.
    await page.context().clearCookies();
    await signIn(page, adminURL, originalEmail, password);
    await page.goto("/admin/settings");
    await expect(profile.getByLabel("Allowed CIDRs")).toHaveValue(fixtureCIDRs);

    const changed = page.waitForResponse(response => response.url().endsWith("/api/admin/profile") && response.request().method() === "PATCH");
    await profile.getByLabel("Email").fill(changedEmail);
    await profile.getByRole("button", { name: "Save administrator email" }).click();
    emailChanged = (await changed).ok();
    expect(emailChanged).toBe(true);
    await expect(profile.getByText("Administrator email updated.")).toBeVisible();

    // A fresh login proves the new identity was persisted independently of
    // the current browser session.
    await page.context().clearCookies();
    await signIn(page, adminURL, changedEmail, password);
  } finally {
    if (emailChanged) {
      await page.context().clearCookies();
      await signIn(page, adminURL, changedEmail, password);
      await page.goto("/admin/settings");
      const restoredProfile = page.getByRole("region", { name: "Administrator profile" });
      await restoredProfile.getByLabel("Email").fill(originalEmail);
      await restoredProfile.getByRole("button", { name: "Save administrator email" }).click();
      await expect(restoredProfile.getByText("Administrator email updated.")).toBeVisible();
    }
    if (policyChanged) {
      await page.context().clearCookies();
      await signIn(page, adminURL, originalEmail, password);
      await page.goto("/admin/settings");
      const restoredPolicy = page.getByRole("region", { name: "Administrator profile" });
      await restoredPolicy.getByLabel("Allowed CIDRs").fill(originalCIDRs);
      await restoredPolicy.getByRole("button", { name: "Save IP allowlist" }).click();
      await expect(restoredPolicy.getByText("IP allowlist updated.")).toBeVisible();
      // Leave the shared authenticated fixture usable by every later spec.
      await page.context().clearCookies();
      await signIn(page, adminURL, originalEmail, password);
    }
  }
});
