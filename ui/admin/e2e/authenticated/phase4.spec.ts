import { readFile } from "node:fs/promises";
import { applyCollectionEdit, createCollection, deleteCollection, expect, test, uniqueName } from "./fixtures";

test("creates and edits advanced fields without persisting password drafts", async ({ page }, info) => {
  const name = uniqueName("advanced", info);
  try {
    await createCollection(page, name, [{ name: "body", type: "editor" }, { name: "place", type: "geoPoint", indexed: true }, { name: "changed", type: "autodate", indexed: true }, { name: "pin", type: "password" }]);
    await page.goto(`/admin/collections/${name}/edit`);
    await expect(page.getByLabel("Field 1 has default")).toBeDisabled();
    await expect(page.getByLabel("Field 2 indexed")).toBeChecked();
    await expect(page.getByLabel("Field 3 set on create")).toBeChecked();
    await expect(page.getByLabel("Field 3 set on update")).toBeChecked();
    await expect(page.getByLabel("Field 4 presentable")).toBeDisabled();
    await page.getByLabel("Field 4 minimum characters").fill("4");
    await applyCollectionEdit(page, name);
    await page.goto(`/admin/collections/${name}/records/new`);
    await page.getByLabel("body state").selectOption("value");
    await page.getByRole("button", { name: "bold", exact: true }).click();
    await page.getByRole("textbox", { name: "body", exact: true }).pressSequentially("Rich content");
    await expect(page.locator(".tiptap strong")).toHaveText("Rich content");
    await page.getByLabel("place state").selectOption("value");
    await page.getByLabel("place longitude").fill("127.5");
    await page.getByLabel("place latitude").fill("37.5");
    await page.getByRole("group", { name: "place coordinate map" }).press("ArrowUp");
    await expect(page.getByLabel("place latitude")).toHaveValue("38.5");
    await page.getByLabel("pin state").selectOption("value");
    await page.getByLabel("pin", { exact: true }).fill("synthetic-private-pin");
    await expect(page.getByLabel("changed", { exact: true })).toHaveAttribute("readonly", "");
    expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain("synthetic-private-pin");
    await page.getByRole("button", { name: "Create record", exact: true }).click();
    await expect(page.getByRole("heading", { name: `${name} records` })).toBeVisible();
    await expect(page.getByRole("cell", { name: "Rich content", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Sort place lat" }).click();
    await expect(page.getByText("The filter or sort expression is invalid.")).toHaveCount(0);
    await page.getByRole("row").filter({ hasText: "Rich content" }).getByRole("link").first().click();
    await expect(page.getByLabel("pin state")).toHaveValue("unset");
    await expect(page.getByLabel("place longitude")).toHaveValue("127.5");
    const before = await page.getByLabel("changed", { exact: true }).inputValue();
    expect(before).not.toBe("");
    await page.getByRole("button", { name: "HTML source", exact: true }).click();
    await expect(page.getByRole("textbox", { name: "body", exact: true })).toHaveValue("<p><strong>Rich content</strong></p>");
    await page.getByRole("textbox", { name: "body", exact: true }).fill("<p>Updated source</p>");
    await page.getByRole("button", { name: "Save changes", exact: true }).click();
    await expect(page.getByRole("heading", { name: `${name} records` })).toBeVisible();
    await page.getByRole("row").filter({ hasText: "Updated source" }).getByRole("link").first().click();
    await expect(page.getByLabel("changed", { exact: true })).not.toHaveValue(before);
    await page.getByRole("button", { name: "Duplicate record", exact: true }).click();
    await expect(page.getByRole("heading", { name: `Create ${name} record` })).toBeVisible();
    await expect(page.getByLabel("pin state")).toHaveValue("unset");
    await expect(page.getByLabel("changed", { exact: true })).toHaveValue("");
    await expect(page.getByRole("textbox", { name: "body", exact: true })).toHaveText("Updated source");
    page.once("dialog", dialog => dialog.accept());
  } finally { await deleteCollection(page, name); }
});

test("moves an existing file local to S3 and back while preserving downloads", async ({ page }, info) => {
  test.skip(!process.env.PGBASE_BROWSER_S3_SETTINGS, "Requires isolated browser server with local MinIO enabled");
  const collection = uniqueName("storage_move", info);
  const content = Buffer.from("storage migration preserves this exact file\n");
  try {
    await createCollection(page, collection, [{ name: "attachment", type: "file" }]);
    await page.goto(`/admin/collections/${collection}/records/new`);
    await page.getByLabel("attachment files for save", { exact: true }).setInputFiles({ name: "move.txt", mimeType: "text/plain", buffer: content });
    const response = page.waitForResponse(value => value.url().endsWith("/api/v1/collections/batch") && value.request().method() === "POST");
    await page.getByRole("button", { name: "Create record", exact: true }).click();
    const created = await (await response).json();
    expect(created.results[0].status).toBe(201);
    const recordURL = `/admin/collections/${collection}/records/${created.results[0].body.id}`;
    async function verifyDownload() {
      await page.goto(recordURL);
      const pending = page.waitForEvent("download");
      await page.getByRole("button", { name: "Download", exact: true }).click();
      const download = await pending;
      expect(await readFile((await download.path())!)).toEqual(content);
    }
    async function migrate() {
      await page.getByRole("button", { name: "Preview migration" }).click();
      const start = page.getByRole("button", { name: /Start migration of [1-9][0-9]* objects/ });
      await expect(start).toBeVisible();
      await start.click();
      await expect(page.getByText("Storage migration completed and active.")).toBeVisible({ timeout: 30000 });
    }
    await verifyDownload();
    await page.goto("/admin/storage");
    await expect(page.getByRole("heading", { name: "File storage" })).toBeVisible();
    await page.getByRole("combobox", { name: "Driver", exact: true }).selectOption("s3");
    await page.getByLabel("Endpoint", { exact: true }).fill("http://127.0.0.1:59000");
    await page.getByLabel("Bucket", { exact: true }).fill("pgbase-test");
    await page.getByLabel("Region", { exact: true }).fill("us-east-1");
    await page.getByLabel("Access key", { exact: true }).fill("pgbase-test-access");
    await page.getByLabel("Secret key", { exact: true }).fill("pgbase-test-secret-0123456789");
    await page.getByLabel("Use path-style addressing").check();
    // The server commits the job, but its response never reaches the browser.
    // Recovery must discover that durable job rather than submit another one.
    let starts = 0;
    await page.route("**/api/admin/storage/files/migrations", async route => {
      starts++;
      const accepted = await route.fetch();
      expect(accepted.status()).toBe(202);
      await route.abort("failed");
    });
    await migrate();
    expect(starts).toBe(1);
    await page.unroute("**/api/admin/storage/files/migrations");
    await expect(page.getByLabel("Secret key", { exact: true })).toHaveValue("");
    await page.reload();
    await expect(page.getByRole("combobox", { name: "Driver", exact: true })).toHaveValue("s3");
    await expect(page.getByLabel("Secret key", { exact: true })).toHaveValue("");
    await page.getByRole("button", { name: "Test saved S3 connection" }).click();
    await expect(page.getByText(/succeeded/i)).toBeVisible();
    await verifyDownload();
    await page.goto("/admin/storage");
    await page.getByRole("combobox", { name: "Driver", exact: true }).selectOption("local");
    await migrate();
    await expect(page.getByRole("combobox", { name: "Driver", exact: true })).toHaveValue("local");
    await verifyDownload();
  } finally { await deleteCollection(page, collection); }
});

test("configures independent S3 backup storage and creates a downloadable archive", async ({ page }, info) => {
  test.skip(!process.env.PGBASE_BROWSER_S3_SETTINGS || process.env.PGBASE_BROWSER_MANAGED_BACKUPS !== "1", "Requires isolated managed server and MinIO");
  test.setTimeout(120_000);
  await page.goto("/admin/storage/backups");
  await expect(page.getByRole("heading", { name: "Backup archive storage" })).toBeVisible();
  await page.getByRole("combobox", { name: "Driver", exact: true }).selectOption("s3");
  await page.getByLabel("Endpoint", { exact: true }).fill("http://127.0.0.1:59000");
  await page.getByLabel("Bucket", { exact: true }).fill("pgbase-test");
  await page.getByLabel("Prefix", { exact: true }).fill(uniqueName("browser_backups", info));
  await page.getByLabel("Region", { exact: true }).fill("us-east-1");
  await page.getByLabel("Access key", { exact: true }).fill("pgbase-test-access");
  await page.getByLabel("Secret key", { exact: true }).fill("pgbase-test-secret-0123456789");
  await page.getByLabel("Use path-style addressing").check();
  await page.getByRole("button", { name: "Save backup storage", exact: true }).click();
  await expect(page.getByText("Backup storage settings saved.", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Secret key", { exact: true })).toHaveValue("");
  await page.reload();
  await expect(page.getByRole("combobox", { name: "Driver", exact: true })).toHaveValue("s3");
  await page.getByRole("button", { name: "Test backup storage", exact: true }).click();
  await expect(page.getByText("Saved backup storage upload, read, and delete succeeded.", { exact: true })).toBeVisible();
  await page.goto("/admin/storage");
  await expect(page.getByRole("combobox", { name: "Driver", exact: true })).toHaveValue("local");
  await page.goto("/admin/system");
  const creation = page.waitForResponse(response => response.url().endsWith("/api/admin/backups") && response.request().method() === "POST");
  await page.getByRole("button", { name: "Create backup", exact: true }).click();
  const job = await (await creation).json();
  await expect(page.getByText(new RegExp(`create backup ${job.backupId}: completed`))).toBeVisible({ timeout: 60000 });
  const pending = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download", exact: true }).first().click();
  const archive = await pending;
  const bytes = await readFile((await archive.path())!);
  expect(bytes.length).toBeGreaterThan(1024);
  expect([...bytes.subarray(0, 2)]).toEqual([0x1f, 0x8b]);
  await page.reload();
  await expect(page.getByRole("button", { name: "Download", exact: true })).toHaveCount(1);
  await page.getByRole("button", { name: "Delete", exact: true }).first().click();
  await expect(page.getByText("No managed backups are available.", { exact: true })).toBeVisible();
  await page.goto("/admin/storage/backups");
  await page.getByRole("combobox", { name: "Driver", exact: true }).selectOption("local");
  await page.getByRole("button", { name: "Save backup storage", exact: true }).click();
  await expect(page.getByText("Backup storage settings saved.", { exact: true })).toBeVisible();
});
