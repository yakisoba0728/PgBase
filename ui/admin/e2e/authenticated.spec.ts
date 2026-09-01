import { expect, test } from "./authenticated-fixtures";

test.describe.configure({ mode: "serial" });

test("the authenticated worker fixture owns a reusable administrator session", async ({ page }) => {
  const email = process.env.PGBASE_ADMIN_EMAIL;
  if (!email) throw new Error("PGBASE_ADMIN_EMAIL is required");

  await page.goto("/admin/");
  await expect(page.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
  await expect(page.getByText(email)).toBeVisible();
});
