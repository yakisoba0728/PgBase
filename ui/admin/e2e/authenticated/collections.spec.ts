import { applyCollectionEdit, createCollection, deleteCollection, expect, fillField, test, uniqueName } from "./fixtures";

test("creates, edits and deletes collections and follows a direct history jump between them", async ({ page }, info) => {
  const first = uniqueName("jump_first", info);
  const second = uniqueName("jump_second", info);

  try {
    await createCollection(page, first, [{ name: "title", indexed: true }]);
    await createCollection(page, second, [{ name: "label", indexed: true }]);

    await page.goto(`/admin/collections/${first}/edit`);
    await expect(page.getByText(`${first} · physical table`)).toBeVisible();
    await page.getByRole("button", { name: "Add field" }).click();
    await fillField(page, 1, { name: "subtitle" });
    await applyCollectionEdit(page, first);

    // A direct URL jump, then back and forward, must swap the whole editor
    // rather than leave one collection's draft or delete plan on the other.
    await page.goto(`/admin/collections/${first}/edit`);
    await expect(page.getByLabel("Field 2 name")).toHaveValue("subtitle");
    await page.getByRole("button", { name: "Delete collection" }).click();
    await expect(page.getByRole("button", { name: "Confirm delete" })).toBeVisible();

    await page.goto(`/admin/collections/${second}/edit`);
    await expect(page.getByText(`${second} · physical table`)).toBeVisible();
    await expect(page.getByRole("button", { name: "Confirm delete" })).toHaveCount(0);
    await expect(page.getByLabel("Field 1 name")).toHaveValue("label");
    await expect(page.getByLabel("Field 2 name")).toHaveCount(0);

    await page.goBack();
    await expect(page.getByLabel("Field 2 name")).toHaveValue("subtitle");
    await expect(page.getByRole("button", { name: "Confirm delete" })).toHaveCount(0);

    await page.goForward();
    await expect(page.getByLabel("Field 1 name")).toHaveValue("label");

    await deleteCollection(page, first);
    await page.goto(`/admin/collections/${first}/edit`);
    await expect(page.getByText("Collection not found.")).toBeVisible();
  } finally {
    await deleteCollection(page, first);
    await deleteCollection(page, second);
  }
});
