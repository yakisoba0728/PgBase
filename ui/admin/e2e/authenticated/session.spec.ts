import { expect, test, signIn } from "./fixtures";

// This flow ends the session it uses, so it starts from an empty context: the
// worker's shared session must survive its own sign-out.
test.use({ storageState: { cookies: [], origins: [] } });

test("signs out and back in against a real backend", async ({ page, baseURL }) => {
  const email = process.env.PGBASE_ADMIN_EMAIL;
  const password = process.env.PGBASE_ADMIN_PASSWORD;
  if (!email || !password || baseURL === undefined) throw new Error("administrator credentials and a baseURL are required");
  const url = new URL("/admin/", baseURL).href;

  await signIn(page, url, email, password);
  await expect(page.getByText(email)).toBeVisible();

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/admin/login");

  await page.reload();
  await expect(page.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();

  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("definitely not the password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByText("Email or password is incorrect.")).toBeVisible();

  await signIn(page, url, email, password);
  await expect(page.getByText(email)).toBeVisible();
});
