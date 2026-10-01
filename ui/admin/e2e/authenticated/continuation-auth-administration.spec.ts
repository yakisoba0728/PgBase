import { test, expect, signIn, uniqueName } from "./fixtures";

test.use({ actionTimeout: 10_000 });

test("manages a second administrator through creation, activation, password replacement and session revocation", async ({ page, browser }, info) => {
  const email = `${uniqueName("admin_ui", info)}@example.test`;
  const initialPassword = "InitialAdminPassword-2026!";
  const replacementPassword = "ReplacementAdminPassword-2026!";
  let createdID: string | undefined;
  await page.goto("/admin/administrators");
  const origin = new URL(page.url()).origin;
  const second = await browser.newContext({ baseURL: origin, storageState: { cookies: [], origins: [] } });
  const other = await second.newPage();
  page.on("dialog", (dialog) => dialog.accept());
  other.on("dialog", (dialog) => dialog.accept());
  try {
    await expect(page.getByRole("heading", { name: "Administrators", exact: true })).toBeVisible();
    const create = page.getByRole("form", { name: "Create administrator" });
    await create.getByLabel("Email", { exact: true }).fill(email);
    await create.getByLabel("Password", { exact: true }).fill(initialPassword);
    const response = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/api/admin/administrators"));
    await create.getByRole("button", { name: "Create administrator" }).click();
    const account = await response;
    expect(account.status()).toBe(201);
    const result = await account.json(); createdID = result.id;
    expect(Object.keys(result).sort()).toEqual(["active", "createdAt", "email", "id", "updatedAt"]);
    await expect(page.getByRole("status")).toContainText("Administrator created");
    await expect(create.getByLabel("Password", { exact: true })).toHaveValue("");
    const row = page.getByRole("row").filter({ has: page.getByRole("cell", { name: email, exact: true }) });
    await signIn(other, `${origin}/admin/`, email, initialPassword);

    await row.getByRole("button", { name: "Deactivate", exact: true }).click();
    await expect(row.getByRole("cell", { name: "Inactive", exact: true })).toBeVisible();
    await other.reload();
    await expect(other.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    await row.getByRole("button", { name: "Activate", exact: true }).click();
    await expect(row.getByRole("cell", { name: "Active", exact: true })).toBeVisible();
    await signIn(other, `${origin}/admin/`, email, initialPassword);

    await row.getByRole("button", { name: "Set password" }).click();
    await page.getByLabel("New password", { exact: true }).fill(replacementPassword);
    await page.getByRole("button", { name: "Save password" }).click();
    await expect(page.getByRole("status")).toContainText("Password replaced");
    await expect(page.getByLabel("New password", { exact: true })).toHaveCount(0);
    await other.reload();
    await expect(other.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    await signIn(other, `${origin}/admin/`, email, replacementPassword);

    await row.getByRole("button", { name: "Revoke sessions" }).click();
    await expect(page.getByRole("status")).toContainText("All sessions revoked");
    await other.reload();
    await expect(other.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    await signIn(other, `${origin}/admin/`, email, replacementPassword);
    await other.getByRole("link", { name: "Administrators", exact: true }).click();
    const own = other.getByRole("row").filter({ has: other.getByRole("cell", { name: `${email} (you)`, exact: true }) });
    await expect(own.getByRole("button", { name: "Deactivate", exact: true })).toBeDisabled();
    await own.getByRole("button", { name: "Revoke sessions" }).click();
    await expect(other.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    await expect(other).toHaveURL(/\/admin\/login$/);
  } finally {
    await second.close();
    if (createdID) {
      const session = await page.request.get(`${origin}/api/admin/session`);
      const csrf = (await session.json()).csrfToken;
      expect((await page.request.patch(`${origin}/api/admin/administrators/${createdID}`, { headers: { Origin: origin, "X-PgBase-CSRF": csrf }, data: { active: false } })).status()).toBe(200);
    }
  }
});

test("edits every native authentication policy field and reloads its server-authoritative values", async ({ page }, info) => {
  const name = uniqueName("policy_ui", info);
  let id: string | undefined;
  await page.goto("/admin/auth-collections");
  const origin = new URL(page.url()).origin;
  try {
    await page.getByLabel("Auth collection name", { exact: true }).fill(name);
    await page.getByLabel("New profile schema", { exact: true }).fill('{"username":"text"}');
    const created = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/api/admin/auth-collections"));
    await page.getByRole("button", { name: "Create auth collection", exact: true }).click();
    const response = await created; expect(response.status()).toBe(201); id = (await response.json()).id;
    const realm = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name, exact: true }) });
    await realm.getByText("Authentication settings", { exact: true }).click();
    await realm.getByLabel("Minimum password bytes", { exact: true }).fill("14");
    await realm.getByLabel("Maximum password bytes", { exact: true }).fill("128");
    await realm.getByLabel("Password pattern", { exact: true }).fill("[A-Z]");
    await realm.getByRole("textbox", { name: "Login identity fields", exact: true }).fill("email\nusername");
    await realm.getByLabel("MFA challenge lifetime in seconds", { exact: true }).fill("120");
    await realm.getByRole("textbox", { name: "MFA rule JSON", exact: true }).fill('{"field":"verified","equals":true}');
    await realm.getByRole("button", { name: "Save policy", exact: true }).click();
    await expect(realm.getByRole("status")).toContainText("Policy saved");
    const saved = await page.request.get(`${origin}/api/admin/auth-collections/${id}/administration`);
    expect(saved.status()).toBe(200);
    expect((await saved.json()).policy).toMatchObject({ minimumPasswordBytes: 14, maximumPasswordBytes: 128, passwordPattern: "[A-Z]", identityFields: ["email", "username"], mfaDurationSeconds: 120, mfaRule: '{"field":"verified","equals":true}' });
    await realm.getByLabel("Password pattern", { exact: true }).fill("[");
    await realm.getByRole("button", { name: "Save policy", exact: true }).click();
    await expect(realm.getByRole("status")).toContainText("Unable to save authentication settings");
    await realm.getByRole("button", { name: "Reload authentication settings", exact: true }).click();
    await realm.getByText("Authentication settings", { exact: true }).click();
    await expect(realm.getByLabel("Maximum password bytes", { exact: true })).toHaveValue("128");
    await expect(realm.getByLabel("Password pattern", { exact: true })).toHaveValue("[A-Z]");
    await expect(realm.getByRole("textbox", { name: "Login identity fields", exact: true })).toHaveValue("email\nusername");
    await expect(realm.getByLabel("MFA challenge lifetime in seconds", { exact: true })).toHaveValue("120");
    await expect(realm.getByRole("textbox", { name: "MFA rule JSON", exact: true })).toHaveValue('{"field":"verified","equals":true}');
  } finally {
    if (id) {
      const session = await page.request.get(`${origin}/api/admin/session`);
      expect((await page.request.delete(`${origin}/api/admin/auth-collections/${id}`, { headers: { Origin: origin, "X-PgBase-CSRF": (await session.json()).csrfToken } })).status()).toBe(204);
    }
  }
});
