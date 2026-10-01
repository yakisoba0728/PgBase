import type { Page } from "@playwright/test";
import { applyCollectionEdit, createCollection, createRecord, deleteCollection, expect, fillField, test, uniqueName } from "./fixtures";

test("preserves custom text IDs through record editing and relation selection", async ({ page }, info) => {
  test.setTimeout(120_000);
  const targets = uniqueName("text_ids", info);
  const posts = uniqueName("text_links", info);
  try {
    await page.goto("/admin/collections/new");
    await page.getByLabel("Collection name").fill(targets);
    await page.getByLabel("Record ID kind").selectOption("text");
    await page.getByLabel("Record ID minimum length").fill("1");
    await page.getByLabel("Record ID maximum length").fill("32");
    await page.getByLabel("Record ID pattern", { exact: true }).fill("^[A-Za-z][A-Za-z0-9_]*$");
    await page.getByLabel("Record ID generation pattern").fill("");
    await fillField(page, 0, { name: "label", indexed: true });
    await applyNewCollection(page);

    await page.goto(`/admin/collections/${targets}/edit`);
    await expect(page.getByLabel("Record ID kind")).toHaveValue("text");
    await expect(page.getByLabel("Record ID kind")).toBeDisabled();
    await expect(page.getByLabel("Record ID minimum length")).toHaveValue("1");
    await expect(page.getByLabel("Record ID maximum length")).toHaveValue("32");
    await expect(page.getByLabel("Record ID pattern", { exact: true })).toHaveValue("^[A-Za-z][A-Za-z0-9_]*$");
    await expect(page.getByLabel("Record ID generation pattern")).toHaveValue("");

    await createTextRecord(page, targets, "CaseSensitive", "Upper-case target");
    await createTextRecord(page, targets, "casesensitive", "Lower-case target");
    await createTextRecord(page, targets, "new", "Route target");
    await expect(page.locator("tbody").getByRole("link", { name: "CaseSensitive", exact: true })).toBeVisible();
    await expect(page.locator("tbody").getByRole("link", { name: "casesensitive", exact: true })).toBeVisible();

    // The literal creation route and a saved record named "new" must remain
    // distinct after following the listing link and after a full reload.
    const reservedID = page.locator("tbody").getByRole("link", { name: "new", exact: true });
    await expect(reservedID).toHaveAttribute("href", `/admin/collections/${targets}/records/%6Eew`);
    await reservedID.click();
    await expect(page.getByRole("heading", { name: `Edit ${targets} record`, exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("heading", { name: `Edit ${targets} record`, exact: true })).toBeVisible();
    await expect(page.getByLabel("label", { exact: true })).toHaveValue("Route target");
    await page.getByLabel("label", { exact: true }).fill("Edited route target");
    await page.getByRole("button", { name: "Save changes", exact: true }).click();
    await expect(page.getByRole("heading", { name: `${targets} records`, exact: true })).toBeVisible();
    await expect(page.locator(".table-scroll tbody tr")).toHaveCount(3);
    await expect(page.getByRole("row").filter({ hasText: "Edited route target" }).getByRole("link", { name: "new", exact: true })).toBeVisible();

    await createCollection(page, posts, [
      { name: "title" },
      { name: "owner", type: "relation", relationTarget: targets },
    ]);
    await page.goto(`/admin/collections/${posts}/records/new`);
    await page.getByLabel("title state").selectOption("value");
    await page.getByLabel("title", { exact: true }).fill("Linked post");
    await page.getByLabel("owner state").selectOption("value");
    await chooseOwner(page, "Upper-case target");
    await expect(page.getByLabel("owner", { exact: true })).toHaveValue("CaseSensitive");
    await page.getByRole("button", { name: "Create record", exact: true }).click();
    await expect(page.getByRole("heading", { name: `${posts} records`, exact: true })).toBeVisible();
    await page.getByRole("row").filter({ hasText: "Linked post" }).getByRole("link").click();
    await expect(page.getByRole("button", { name: /^Remove Upper-case target/ })).toBeVisible();
    await expect(page.getByLabel("owner", { exact: true })).toHaveValue("CaseSensitive");

    await page.getByRole("button", { name: /^Remove Upper-case target/ }).click();
    await chooseOwner(page, "Edited route target");
    await expect(page.getByLabel("owner", { exact: true })).toHaveValue("new");
    await page.getByRole("button", { name: "Save changes", exact: true }).click();
    await expect(page.getByRole("heading", { name: `${posts} records`, exact: true })).toBeVisible();
    await page.getByRole("row").filter({ hasText: "Linked post" }).getByRole("link").click();
    await expect(page.getByRole("button", { name: /^Remove Edited route target/ })).toBeVisible();
    await expect(page.getByLabel("owner", { exact: true })).toHaveValue("new");
  } finally {
    await deleteCollection(page, posts);
    await deleteCollection(page, targets);
  }
});

test("persists user access for collections with incoming and outgoing relations", async ({ page }, info) => {
  const targets = uniqueName("access_targets", info);
  const posts = uniqueName("access_posts", info);
  const collectionItem = (name: string) => page.getByRole("listitem").filter({ has: page.getByRole("link", { name, exact: true }) });
  try {
    await createCollection(page, targets, [{ name: "label" }]);
    await createCollection(page, posts, [{ name: "owner", type: "relation", relationTarget: targets }]);
    for (const name of [targets, posts]) {
      await collectionItem(name).getByRole("button", { name: "Enable user access", exact: true }).click();
      await expect(collectionItem(name).getByRole("button", { name: "Disable user access", exact: true })).toBeEnabled();
    }
    await page.reload();
    for (const name of [targets, posts]) {
      await expect(collectionItem(name).getByRole("button", { name: "Disable user access", exact: true })).toBeEnabled();
    }
    await collectionItem(posts).getByRole("button", { name: "Disable user access", exact: true }).click();
    await expect(collectionItem(posts).getByRole("button", { name: "Enable user access", exact: true })).toBeEnabled();
    await page.reload();
    await expect(collectionItem(posts).getByRole("button", { name: "Enable user access", exact: true })).toBeEnabled();
    await expect(collectionItem(targets).getByRole("button", { name: "Disable user access", exact: true })).toBeEnabled();
  } finally {
    await deleteCollection(page, posts);
    await deleteCollection(page, targets);
  }
});

test("previews and applies projection and filter edits to a builder view", async ({ page }, info) => {
  const source = uniqueName("builder_source", info);
  const view = uniqueName("builder_view", info);
  try {
    await createCollection(page, source, [{ name: "title" }, { name: "status" }]);
    await createRecord(page, source, { title: "Draft item", status: "draft" });
    await createRecord(page, source, { title: "Published item", status: "published" });
    await page.goto("/admin/collections/new");
    await page.getByLabel("Collection name").fill(view);
    await page.getByLabel("Collection kind").selectOption("view");
    await page.getByLabel("Source collection").selectOption(source);
    await page.getByLabel("Project title", { exact: true }).check();
    await page.getByRole("combobox", { name: "Static filter", exact: true }).selectOption("compare");
    await page.getByRole("combobox", { name: "Filter field", exact: true }).selectOption("status");
    await page.getByLabel("Filter value", { exact: true }).fill("draft");
    await applyNewCollection(page);
    await page.goto(`/admin/collections/${view}/records`);
    await expect(page.getByRole("cell", { name: "Draft item", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "Published item", exact: true })).toHaveCount(0);

    await page.goto(`/admin/collections/${view}/edit`);
    await expect(page.getByLabel("Definition mode")).toHaveValue("builder");
    await expect(page.getByLabel("Definition mode")).toBeDisabled();
    await expect(page.getByLabel("Project title", { exact: true })).toBeChecked();
    await page.getByLabel("Project title", { exact: true }).uncheck();
    await page.getByLabel("Project status", { exact: true }).check();
    await page.getByRole("combobox", { name: "Comparison", exact: true }).selectOption("eq");
    await page.getByLabel("Filter value", { exact: true }).fill("published");
    await applyCollectionEdit(page, view);
    await expect(page.getByRole("columnheader", { name: "status", exact: true })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "title", exact: true })).toHaveCount(0);
    await expect(page.getByRole("cell", { name: "published", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "draft", exact: true })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Create record", exact: true })).toHaveCount(0);

    await page.goto(`/admin/collections/${view}/edit`);
    await expect(page.getByLabel("Source collection")).toHaveValue(source);
    await expect(page.getByLabel("Project title", { exact: true })).not.toBeChecked();
    await expect(page.getByLabel("Project status", { exact: true })).toBeChecked();
    await expect(page.getByRole("combobox", { name: "Filter field", exact: true })).toHaveValue("status");
    await expect(page.getByRole("combobox", { name: "Comparison", exact: true })).toHaveValue("eq");
    await expect(page.getByLabel("Filter value", { exact: true })).toHaveValue("published");
  } finally {
    await deleteCollection(page, view);
    await deleteCollection(page, source);
  }
});

test("applies a text ID policy to an auth scaffold and preserves exact auth record IDs", async ({ page }, info) => {
  const name = uniqueName("auth_text", info);
  const publicID = `Auth_${name}`;
  const policy = { kind: "text", minLength: 1, maxLength: 64, pattern: "^[A-Za-z][A-Za-z0-9_]*$" };
  let authorityID: string | undefined;
  let headers: Record<string, string> = {};
  let origin = "";
  let createdUser = false;
  let completed = false;
  try {
    await page.goto("/admin/collections/new/auth");
    await expect(page.getByLabel("Field 1 name")).toHaveValue("password");
    await page.getByLabel("Collection name").fill(name);
    await page.getByRole("combobox", { name: "Record ID kind", exact: true }).selectOption("text");
    await page.getByLabel("Record ID minimum length").fill(String(policy.minLength));
    await page.getByLabel("Record ID maximum length").fill(String(policy.maxLength));
    await page.getByLabel("Record ID pattern", { exact: true }).fill(policy.pattern);
    await page.getByLabel("Record ID generation pattern").fill("");
    await applyNewCollection(page);

    // Auth identities use their own authority, so a saved catalog policy alone
    // does not prove that real users inherit the designer's text ID policy.
    origin = new URL(page.url()).origin;
    const sessionResponse = await page.request.get(`${origin}/api/admin/session`);
    expect(sessionResponse.status()).toBe(200);
    headers = { Origin: origin, "X-PgBase-CSRF": (await sessionResponse.json()).csrfToken };
    const authoritiesResponse = await page.request.get(`${origin}/api/admin/auth-collections`);
    expect(authoritiesResponse.status()).toBe(200);
    const authorities: { items: Array<{ id: string; name: string; recordId?: typeof policy }> } = await authoritiesResponse.json();
    const authority = authorities.items.find(item => item.name === name);
    expect(authority).toBeDefined();
    authorityID = authority!.id;
    expect(authority!.recordId).toEqual(policy);

    const created = await page.request.post(`${origin}/api/admin/auth-collections/${authorityID}/records`, {
      headers,
      data: { id: publicID, email: `${name}@example.test`, password: "AuthRecordPassword-2026!", fields: {} },
    });
    createdUser = created.status() === 201;
    expect(created.status()).toBe(201);
    expect(await created.json()).toMatchObject({ id: publicID, authCollectionId: authorityID });
    const saved = await page.request.get(`${origin}/api/admin/auth-collections/${authorityID}/records/${publicID}`);
    expect(saved.status()).toBe(200);
    expect(await saved.json()).toMatchObject({ id: publicID, email: `${name}@example.test` });

    const exportedResponse = await page.request.get(`${origin}/api/admin/schema/export`);
    expect(exportedResponse.status()).toBe(200);
    const exported: { collections: Array<{ name: string; recordId?: typeof policy }>; authCollections: Array<{ name: string; recordId?: typeof policy }> } = await exportedResponse.json();
    expect(exported.collections.find(item => item.name === name)?.recordId).toEqual(policy);
    expect(exported.authCollections.find(item => item.name === name)?.recordId).toEqual(policy);
    completed = true;
  } finally {
    if (!page.isClosed()) {
      try {
        if (authorityID !== undefined) {
          if (createdUser) expect((await page.request.delete(`${origin}/api/admin/auth-collections/${authorityID}/records/${publicID}`, { headers })).status()).toBe(204);
          expect((await page.request.delete(`${origin}/api/admin/auth-collections/${authorityID}`, { headers })).status()).toBe(204);
        }
        await deleteCollection(page, name);
      } catch (error) {
        if (completed) throw error;
        info.annotations.push({ type: "cleanup", description: String(error) });
      }
    }
  }
});

async function applyNewCollection(page: Page): Promise<void> {
  const done = page.getByRole("heading", { name: "Collections", exact: true });
  const moved = page.getByRole("alert").filter({ hasText: "The catalog changed" });
  for (let attempt = 0; attempt < 5; attempt += 1) {
    await page.getByRole("button", { name: "Preview collection", exact: true }).click();
    await page.getByRole("button", { name: "Apply collection", exact: true }).click();
    await expect(done.or(moved).first()).toBeVisible();
    if (await done.count() > 0) return;
  }
  throw new Error("creating the collection kept losing a catalog race");
}

async function createTextRecord(page: Page, collection: string, id: string, label: string): Promise<void> {
  await page.goto(`/admin/collections/${collection}/records/new`);
  await page.getByLabel("Record ID", { exact: true }).fill(id);
  await page.getByLabel("label state").selectOption("value");
  await page.getByLabel("label", { exact: true }).fill(label);
  await page.getByRole("button", { name: "Create record", exact: true }).click();
  await expect(page.getByRole("heading", { name: `${collection} records`, exact: true })).toBeVisible();
  await expect(page.locator("tbody").getByRole("link", { name: id, exact: true })).toBeVisible();
}

async function chooseOwner(page: Page, label: string): Promise<void> {
  await page.getByLabel("owner target label").fill(label);
  await page.getByRole("button", { name: "Search owner targets", exact: true }).click();
  await page.getByRole("option", { name: new RegExp(`^${label}`) }).click();
}
