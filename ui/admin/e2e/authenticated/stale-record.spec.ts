import { createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "./fixtures";

test("recovers a second tab from the precondition the first tab invalidated", async ({ page, context }, info) => {
  const notes = uniqueName("stale_notes", info);

  try {
    await createCollection(page, notes, [{ name: "body", indexed: true }]);
    await createRecord(page, notes, { body: "original" });
    await page.getByRole("link", { name: /^[0-9a-f-]{36}$/ }).click();
    await expect(page.getByRole("heading", { name: `Edit ${notes} record` })).toBeVisible();
    const recordURL = page.url();

    const second = await context.newPage();
    await second.goto(recordURL);
    await expect(second.getByLabel("body", { exact: true })).toHaveValue("original");

    // The first tab moves the record on, so the second tab now holds an ETag
    // the server will refuse.
    await page.getByLabel("body", { exact: true }).fill("first tab wins");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByRole("heading", { name: `${notes} records` })).toBeVisible();

    await second.getByLabel("body", { exact: true }).fill("second tab draft");
    await second.getByRole("button", { name: "Save changes" }).click();
    await expect(second.getByRole("alert")).toContainText("This record changed after you loaded it.");
    await expect(second.getByLabel("body", { exact: true })).toHaveValue("second tab draft");
    await expect(second.getByRole("button", { name: "Save changes" })).toBeDisabled();

    await second.getByRole("button", { name: "Reload record" }).click();
    await expect(second.getByLabel("body", { exact: true })).toHaveValue("second tab draft");
    await expect(second.getByRole("button", { name: "Save changes" })).toBeEnabled();

    await second.getByLabel("body", { exact: true }).fill("second tab after reload");
    await second.getByRole("button", { name: "Save changes" }).click();
    await expect(second.getByRole("heading", { name: `${notes} records` })).toBeVisible();
    await expect(second.getByText("second tab after reload")).toBeVisible();
    await second.close();
  } finally {
    await deleteCollection(page, notes);
  }
});
