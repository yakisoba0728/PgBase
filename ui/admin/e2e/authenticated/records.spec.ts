import { createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "./fixtures";

test("creates, edits, filters and sorts scalar and relation records", async ({ page }, info) => {
  const targets = uniqueName("rec_targets", info);
  const posts = uniqueName("rec_posts", info);

  try {
    await createCollection(page, targets, [{ name: "label", indexed: true }]);
    await createRecord(page, targets, { label: "alpha" });
    await createCollection(page, posts, [
      { name: "title", indexed: true },
      { name: "owner", type: "relation", relationTarget: targets },
    ]);

    const targetID = await ownerID(page, targets);
    await page.goto(`/admin/collections/${posts}/records/new`);
    await page.getByLabel("title state").selectOption("value");
    await page.getByLabel("title", { exact: true }).fill("second post");
    await page.getByLabel("owner state").selectOption("value");
    // The textbox drives the listbox: Enter searches, the arrow key activates
    // the first result and a second Enter chooses it, all without leaving it.
    await page.getByLabel("owner target label").fill("alpha");
    await page.getByLabel("owner target label").press("Enter");
    await expect(page.getByRole("option", { name: /^alpha/ })).toBeVisible();
    await page.getByLabel("owner target label").press("ArrowDown");
    await page.getByLabel("owner target label").press("Enter");
    await expect(page.getByLabel("owner", { exact: true })).toHaveValue(targetID);
    await expect(page.getByRole("button", { name: new RegExp(`^Remove alpha`) })).toBeVisible();
    await page.getByRole("button", { name: "Create record" }).click();
    await expect(page.getByRole("heading", { name: `${posts} records` })).toBeVisible();

    await createRecord(page, posts, { title: "first post" });
    await expect(page.getByText("first post")).toBeVisible();
    await expect(page.getByText("second post")).toBeVisible();

    // Only the id column is a link, so the row is what identifies the record.
    await page.getByRole("row").filter({ hasText: "first post" }).getByRole("link").first().click();
    await expect(page.getByRole("heading", { name: `Edit ${posts} record` })).toBeVisible();
    await page.getByLabel("title", { exact: true }).fill("edited post");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("edited post")).toBeVisible();

    // Reopening the saved record is the only path that makes the editor
    // resolve a stored id to a label, so this is what exercises the
    // id-equality hydration filter against the real filter parser.
    await page.getByRole("row").filter({ hasText: "second post" }).getByRole("link").first().click();
    await expect(page.getByRole("heading", { name: `Edit ${posts} record` })).toBeVisible();
    await expect(page.getByRole("button", { name: new RegExp(`^Remove alpha`) })).toBeVisible();
    await page.goBack();

    await page.getByLabel("Filter").fill("title = 'second post'");
    await page.getByRole("button", { name: "Apply filter" }).click();
    await expect(page.getByText("second post")).toBeVisible();
    await expect(page.getByText("edited post")).toHaveCount(0);

    await page.getByLabel("Filter").fill("ghost_field = 'x'");
    await page.getByRole("button", { name: "Apply filter" }).click();
    await expect(page.getByText("The filter or sort expression is invalid.")).toBeVisible();

    await page.getByRole("button", { name: "Clear" }).click();
    await expect(page.getByText("edited post")).toBeVisible();

    const titles = page.locator("tbody tr td:nth-child(4)");
    await page.getByRole("button", { name: /^title/ }).click();
    await expect(page.getByRole("columnheader", { name: "title ↑" })).toHaveAttribute("aria-sort", "ascending");
    await expect(titles).toHaveText(["edited post", "second post"]);

    await page.getByRole("button", { name: "title ↑" }).click();
    await expect(page.getByRole("columnheader", { name: "title ↓" })).toHaveAttribute("aria-sort", "descending");
    await expect(titles).toHaveText(["second post", "edited post"]);
  } finally {
    await deleteCollection(page, posts);
    await deleteCollection(page, targets);
  }
});

async function ownerID(page: import("@playwright/test").Page, collection: string): Promise<string> {
  await page.goto(`/admin/collections/${collection}/records`);
  const id = await page.locator("tbody tr td:first-child a").first().innerText();
  expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
  return id;
}
