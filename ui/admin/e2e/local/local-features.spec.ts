import { createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "../authenticated/fixtures";

test("creates a compound index and a filtered read-only view", async ({ page }, info) => {
  const source = uniqueName("view_source", info), view = uniqueName("view_projection", info);
  try {
    await createCollection(page, source, [{ name: "title" }, { name: "category" }]);
    await page.goto(`/admin/collections/${source}/edit`);
    await page.getByRole("button", { name: "Add compound index" }).click();
    await page.getByLabel("Index name").fill("by_category_title");
    await page.getByLabel("Index 1 fields").fill("category, title");
    await page.getByRole("button", { name: "Preview change" }).click();
    await page.getByRole("button", { name: "Apply change" }).click();
    await expect(page.getByRole("heading", { name: `${source} records` })).toBeVisible();
    await createRecord(page, source, { title: "visible", category: "public" });
    await createRecord(page, source, { title: "hidden", category: "private" });
    await page.goto("/admin/collections/new");
    await page.getByLabel("Collection name").fill(view);
    await page.getByLabel("Collection kind").selectOption("view");
    await page.getByLabel("Source collection").selectOption(source);
    await page.getByLabel("Project title").check();
    await page.getByLabel("Static filter").selectOption("compare");
    await page.getByLabel("Filter field").selectOption("category");
    await page.getByLabel("Filter value").fill("public");
    await page.getByRole("button", { name: "Preview collection" }).click();
    await page.getByRole("button", { name: "Apply collection" }).click();
    await expect(page.getByRole("heading", { name: "Collections", exact: true })).toBeVisible();
    await page.goto(`/admin/collections/${view}/records`);
    await expect(page.getByText("visible", { exact: true })).toBeVisible();
    await expect(page.getByText("hidden", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Create record" })).toHaveCount(0);
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  } finally {
    await deleteCollection(page, view);
    await deleteCollection(page, source);
  }
});

test("manages an auth realm and uploads a local record attachment", async ({ page }, info) => {
  const realm = uniqueName("realm", info), documents = uniqueName("documents", info);
  await page.goto("/admin/auth-collections");
  await page.getByLabel("Auth collection name").fill(realm);
  await page.getByLabel("New profile schema").fill('{"nickname":"text"}');
  await page.getByRole("button", { name: "Create auth collection" }).click();
  const row = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name: realm, exact: true }) });
  await expect(row).toBeVisible();
  await row.getByLabel("Registration enabled").click();
  await expect(row.getByLabel("Registration enabled")).toBeChecked();
  await page.reload();
  await expect(row.getByLabel("Registration enabled")).toBeChecked();
  await row.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(row).toHaveCount(0);
  try {
    await createCollection(page, documents, [{ name: "title" }, { name: "attachment", type: "file" }]);
    await createRecord(page, documents, { title: "attachment record" });
    await page.getByRole("row").filter({ hasText: "attachment record" }).getByRole("link").first().click();
    const field = page.getByRole("group", { name: "attachment files" });
    await field.locator('input[type="file"]').setInputFiles({ name: "browser.txt", mimeType: "text/plain", buffer: Buffer.from("browser local attachment") });
    await field.getByRole("button", { name: "Upload", exact: true }).click();
    await expect(field.getByText(/browser.txt/)).toBeVisible();
    const download = page.waitForEvent("download");
    await field.getByRole("button", { name: "Download" }).click();
    expect((await download).suggestedFilename()).toBe("browser.txt");
    await field.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(field.getByText(/browser.txt/)).toHaveCount(0);
  } finally { await deleteCollection(page, documents); }
});

test("uses the local browser SDK example for registration, notes and files", async ({ page }) => {
  // This local-only project requires examples/user-notes/server.js on port 5173.
  const catalogResponse = await page.request.get("/api/admin/collections");
  const catalog = await catalogResponse.json();
  const existed = Object.values(catalog.collections as Record<string, { name: string }>).some((collection) => collection.name === "notes");
  if (existed) throw new Error("Use a disposable local project without a notes collection for the example drill");
  try {
    await createCollection(page, "notes", [{ name: "title" }, { name: "attachment", type: "file" }]);
    const session = await (await page.request.get("/api/admin/session")).json();
    const origin = new URL(page.url()).origin;
    const enabled = await page.request.put("/api/admin/collections/notes/user-access", { data: { enabled: true }, headers: { Origin: origin, "X-PgBase-CSRF": session.csrfToken } });
    expect(enabled.ok()).toBe(true);
    await page.goto("http://127.0.0.1:5173");
    await page.getByLabel("Email", { exact: true }).fill(`browser-sdk-${Date.now()}@example.test`);
    await page.getByLabel("Password", { exact: true }).fill("browser-sdk-password-0123456789");
    await page.getByRole("button", { name: "Register", exact: true }).click();
    await expect(page.locator("#note-form")).toBeVisible();
    await page.getByLabel("New note").fill("browser SDK note");
    await page.getByRole("button", { name: "Save note" }).click();
    await expect(page.locator("#notes")).toContainText("browser SDK note");
    await page.getByRole("button", { name: "Choose for file upload" }).click();
    await page.getByLabel("Attachment", { exact: true }).setInputFiles({ name: "example.txt", mimeType: "text/plain", buffer: Buffer.from("SDK browser upload") });
    await page.getByRole("button", { name: "Upload file", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("File uploaded");
    await page.getByRole("button", { name: "Log out", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Signed out");
  } finally { await deleteCollection(page, "notes"); }
});

test("issues and revokes a scoped service token", async ({ page }, info) => {
  const collection = uniqueName("token_scope", info), name = uniqueName("worker", info);
  try {
    await createCollection(page, collection, [{ name: "title" }]);
    const catalog = await (await page.request.get("/api/admin/collections")).json();
    const definition = Object.values(catalog.collections as Record<string, { name: string; id: string }>).find((item) => item.name === collection)!;
    await page.goto("/admin/service-tokens");
    await page.getByLabel("Name", { exact: true }).fill(name);
    await page.getByLabel("Scopes (comma separated UUID:action)").fill(`${definition.id}:read`);
    await page.getByRole("button", { name: "Issue token", exact: true }).click();
    await expect(page.getByRole("region", { name: "New service token" })).toBeVisible();
    await page.getByRole("button", { name: "I copied the token" }).click();
    await page.reload();
    await expect(page.getByRole("region", { name: "New service token" })).toHaveCount(0);
    const row = page.getByRole("listitem").filter({ hasText: name });
    await expect(row).toContainText("Active");
    await row.getByRole("button", { name: "Revoke", exact: true }).click();
    await expect(row).toContainText("Revoked");
    await page.reload();
    await expect(row).toContainText("Revoked");
  } finally { await deleteCollection(page, collection); }
});
