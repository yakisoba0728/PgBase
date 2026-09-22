import type { Page } from "@playwright/test";
import { createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "./fixtures";

async function saveSettings(page: Page) {
  const response = page.waitForResponse((value) => value.url().endsWith("/api/admin/settings") && value.request().method() === "PATCH");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  expect((await response).status()).toBe(200);
  await expect(page.getByText("Settings saved.", { exact: true })).toBeVisible();
}

test("system appearance follows OS changes while explicit themes remain selected", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/admin/settings");
  const theme = page.getByRole("combobox", { name: "Theme", exact: true });
  const original = await theme.inputValue();
  try {
    await theme.selectOption("system");
    await saveSettings(page);
    await expect(page.locator("html")).toHaveCSS("background-color", "rgb(17, 24, 39)");
    await page.emulateMedia({ colorScheme: "light" });
    await expect(page.locator("html")).toHaveCSS("background-color", "rgb(245, 247, 251)");
    await theme.selectOption("dark");
    await saveSettings(page);
    await expect(page.locator("html")).toHaveCSS("background-color", "rgb(17, 24, 39)");
    await page.emulateMedia({ colorScheme: "dark" });
    await theme.selectOption("light");
    await saveSettings(page);
    await expect(page.locator("html")).toHaveCSS("background-color", "rgb(245, 247, 251)");
    await theme.selectOption("system");
    await saveSettings(page);
    await page.reload();
    await expect(theme).toHaveValue("system");
    await expect(page.locator("html")).toHaveCSS("background-color", "rgb(17, 24, 39)");
  } finally {
    await theme.selectOption(original);
    await saveSettings(page);
  }
});

test("saves rules and previews only the selected unsaved action against the actual server", async ({ page }, info) => {
  const name = uniqueName("native_rules", info);
  try {
    await createCollection(page, name, [{ name: "title" }]);
    const collection = page.getByRole("listitem").filter({ has: page.getByRole("link", { name, exact: true }) });
    await collection.getByRole("button", { name: "Enable user access", exact: true }).click();
    await expect(collection.getByRole("button", { name: "Disable user access", exact: true })).toBeEnabled();
    await createRecord(page, name, { title: "Rule preview target" });
    const recordID = await page.getByRole("row").filter({ hasText: "Rule preview target" }).getByRole("link").innerText();
    await page.goto(`/admin/collections/${name}/edit`);
    await page.getByRole("combobox", { name: "view", exact: true }).selectOption("allow");
    const saved = page.waitForResponse((response) => response.url().endsWith(`/collections/${name}/rules`) && response.request().method() === "PUT");
    await page.getByRole("button", { name: "Save rules", exact: true }).click();
    expect((await saved).status()).toBe(200);
    await page.reload();
    await expect(page.getByRole("combobox", { name: "view", exact: true })).toHaveValue("allow");
    await page.getByLabel("Record IDs (comma or space separated)").fill(recordID);
    for (const [rule, decision] of [["deny", "denied"], ["allow", "allowed"]]) {
      await page.getByRole("combobox", { name: "view", exact: true }).selectOption(rule);
      const preview = page.waitForResponse((response) => response.url().endsWith(`/collections/${name}/rules/preview`));
      await page.getByRole("button", { name: "Preview access", exact: true }).click();
      expect((await preview).status()).toBe(200);
      await expect(page.getByLabel("Rule preview results")).toHaveText(`${recordID}: ${decision}`);
    }
  } finally {
    await deleteCollection(page, name);
  }
});
