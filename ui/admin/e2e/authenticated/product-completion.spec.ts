import { createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "./fixtures";

test("completes the record productivity workflow", async ({ page }, info) => {
  test.setTimeout(120_000);
  const name = uniqueName("product_records", info);
  try {
    await createCollection(page, name, [{ name: "title" }, { name: "status" }]);
    await createRecord(page, name, { title: "first", status: "draft" });
    await createRecord(page, name, { title: "second", status: "draft" });

    const first = page.getByRole("row").filter({ hasText: "first" });
    await first.getByRole("link").click();
    await page.getByLabel("title", { exact: true }).fill("unsaved local draft");
    await page.reload();
    await expect(page.getByLabel("title", { exact: true })).toHaveValue("unsaved local draft");
    page.once("dialog", dialog => dialog.accept());
    await page.getByRole("button", { name: "Duplicate record" }).click();
    await expect(page.getByRole("heading", { name: `Create ${name} record` })).toBeVisible();
    await expect(page.getByLabel("title", { exact: true })).toHaveValue("unsaved local draft");
    await page.getByRole("button", { name: "Create record" }).click();

    await page.getByLabel(/Select record/).first().check();
    await page.evaluate(() => { const original = URL.createObjectURL; URL.createObjectURL = (blob) => { (window as unknown as { exportedBlob: Blob }).exportedBlob = blob; return original(blob); }; });
    await page.getByRole("button", { name: /Export 1 selected as JSON/ }).click();
    await expect(page.getByText("Exported 1 selected record.")).toBeVisible();
    const exported = JSON.parse(await page.evaluate(() => (window as unknown as { exportedBlob: Blob }).exportedBlob.text()));
    expect(exported).toHaveLength(1);
    page.once("dialog", dialog => dialog.accept());
    await page.getByRole("button", { name: "Delete 1 selected" }).click();
    await expect(page.getByText(/Deleted 1 record/)).toBeVisible();

    await page.goto(`/admin/collections/${name}/edit`);
    await page.getByLabel("Field 1 name").fill("headline");
    const planResponse = page.waitForResponse(response => response.url().endsWith("/api/admin/collections/plan"));
    await page.getByRole("button", { name: "Preview change" }).click();
    await planResponse;
    await expect(page.getByRole("button", { name: "Apply change" })).toBeVisible();
    await page.getByRole("button", { name: "Apply change" }).click();
    await expect(page.getByRole("heading", { name: `${name} records` })).toBeVisible();
    await expect(page.getByText("second", { exact: true })).toBeVisible();

    await page.goto(`/admin/collections/${name}/edit`);
    await page.getByLabel(/Field 2 allowed values/).fill('["draft", "published"]');
    await page.getByRole("button", { name: "Preview change" }).click();
    await expect(page.getByRole("button", { name: "Apply change" })).toBeVisible();
    await page.getByRole("button", { name: "Apply change" }).click();
    await expect(page.getByRole("heading", { name: `${name} records` })).toBeVisible();
    const invalidStatus = await page.evaluate(async ({ collection }) => {
      const session = await fetch("/api/admin/session").then(response => response.json());
      return (await fetch(`/api/v1/collections/${collection}/records`, { method: "POST", headers: { "Content-Type": "application/json", "X-PgBase-CSRF": session.csrfToken }, body: JSON.stringify({ headline: "invalid", status: "forbidden" }) })).status;
    }, { collection: name });
    expect(invalidStatus).toBe(422);

    await page.goto("/admin/system");
    await expect(page.getByRole("button", { name: "Refresh operations" })).toBeVisible();
    await page.getByRole("button", { name: "Refresh operations" }).click();
    await page.goto("/admin/auth-collections");
    await expect(page.getByRole("heading", { name: "Auth collections" })).toBeVisible();
    const users = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name: "users", exact: true }) });
    await users.getByText("Authentication settings", { exact: true }).click();
    await expect(users.getByRole("group", { name: "Password and MFA policy" })).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  } finally { await deleteCollection(page, name); }
});
