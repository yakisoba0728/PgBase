import { mkdir } from "node:fs/promises";
import path from "node:path";
import { createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "./fixtures";

const evidence = path.resolve("../../.local/ui-rebuild-evidence");

test("desktop primary navigation stays visible and clickable beside the brand", async ({ page }) => {
  await page.goto("/admin/collections");
  for (const width of [1440, 1024]) {
    await page.setViewportSize({ width, height: 1000 });
    const brand = page.getByRole("heading", { name: "pgBase administration", exact: true });
    expect((await brand.boundingBox())!.height).toBeLessThan(32);
    for (const name of ["Collections section", "Logs section", "Settings section"]) {
      const link = page.getByRole("navigation", { name: "Primary sections", exact: true }).getByRole("link", { name, exact: true });
      await expect(link).toBeVisible();
      expect(await link.evaluate(element => {
        const box = element.getBoundingClientRect();
        const point = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
        return box.x >= 0 && box.y >= 0 && box.right <= innerWidth && box.bottom <= innerHeight && !!point && element.contains(point);
      })).toBe(true);
    }
  }
});

test("collection tabs reveal invalid fields and keep drafts across sections", async ({ page }, info) => {
  const name = uniqueName("ui_invalid", info);
  const browserErrors: string[] = [];
  page.on("console", message => { if (message.type() === "error") browserErrors.push(message.text()); });
  await page.goto("/admin/collections/new");
  await page.getByLabel("Collection name", { exact: true }).fill(name);
  await page.getByRole("tab", { name: "Options", exact: true }).click();
  await page.getByRole("button", { name: "Preview collection", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Fields", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByLabel("Field 1 name", { exact: true })).toBeFocused();
  expect(browserErrors.filter(message => message.includes("not focusable"))).toEqual([]);
  await page.getByLabel("Field 1 name", { exact: true }).fill("title");
  await page.getByRole("tab", { name: "Indexes", exact: true }).click();
  await page.getByRole("tab", { name: "Fields", exact: true }).click();
  await expect(page.getByLabel("Field 1 name", { exact: true })).toHaveValue("title");
});

test("rebuilt workspace supports real record writes, filters, themes and mobile navigation", async ({ page }, info) => {
  const name = uniqueName("ui_records", info);
  await mkdir(evidence, { recursive: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  try {
    await createCollection(page, name, [{ name: "title", indexed: true }]);
    await createRecord(page, name, { title: "First browser record" });
    await expect(page.getByRole("cell", { name: "First browser record", exact: true })).toBeVisible();
    await page.getByRole("row").filter({ hasText: "First browser record" }).getByRole("link").click();
    await page.getByLabel("title", { exact: true }).fill("Updated browser record");
    await page.getByRole("button", { name: "Save changes", exact: true }).click();
    await expect(page.getByRole("cell", { name: "Updated browser record", exact: true })).toBeVisible();
    await page.getByLabel("Filter", { exact: true }).fill("title = 'Updated browser record'");
    await page.getByRole("button", { name: "Apply filter", exact: true }).click();
    await expect(page.getByRole("cell", { name: "Updated browser record", exact: true })).toBeVisible();
    await page.screenshot({ path: path.join(evidence, "records-desktop-light.png"), fullPage: true });
    await page.evaluate(() => { document.documentElement.dataset.theme = "dark"; });
    await expect(page.getByRole("link", { name: "API preview", exact: true })).toHaveCSS("color", "rgb(231, 235, 239)");
    await page.screenshot({ path: path.join(evidence, "records-desktop-dark.png"), fullPage: true });
    await page.evaluate(() => { document.documentElement.dataset.theme = "light"; });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByRole("button", { name: "Toggle navigation", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Toggle navigation", exact: true }).click();
    await expect(page.getByRole("button", { name: "Close navigation", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Toggle navigation", exact: true })).toBeFocused();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: path.join(evidence, "records-mobile-light.png"), fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("/admin/settings");
    await expect(page.getByRole("heading", { name: "Application settings", exact: true })).toBeVisible();
    await page.getByRole("tab", { name: "My account", exact: true }).click();
    await expect(page.getByRole("tabpanel", { name: "My account", exact: true })).toBeVisible();
    await page.screenshot({ path: path.join(evidence, "settings-desktop.png"), fullPage: true });
    await page.goto("/admin/system");
    await expect(page.getByRole("heading", { name: /System/ }).first()).toBeVisible();
    await page.screenshot({ path: path.join(evidence, "system-desktop.png"), fullPage: true });
  } finally {
    page.on("dialog", dialog => dialog.accept());
    await deleteCollection(page, name);
  }
});
