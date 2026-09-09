import { applyCollectionEdit, createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "./fixtures";

test("persists field presentation settings and shows input help", async ({ page }, info) => {
  const name = uniqueName("presentation", info);
  try {
    await createCollection(page, name, [{ name: "title" }, { name: "private_note" }]);
    await page.goto(`/admin/collections/${name}/edit`);
    await page.getByLabel("Field 1 presentable").check();
    await page.getByLabel("Field 1 help").fill("A short name for this item.");
    await page.getByLabel("Field 2 hidden").check();
    await applyCollectionEdit(page, name);
    await page.goto(`/admin/collections/${name}/edit`);
    await expect(page.getByLabel("Field 1 presentable")).toBeChecked();
    await expect(page.getByLabel("Field 1 help")).toHaveValue("A short name for this item.");
    await expect(page.getByLabel("Field 2 hidden")).toBeChecked();
    await expect(page.getByLabel("Field 2 presentable")).toBeDisabled();
    await page.goto(`/admin/collections/${name}/records/new`);
    await expect(page.getByText("A short name for this item.", { exact: true })).toBeVisible();
    await createRecord(page, name, { title: "Visible title", private_note: "Administrator value" });
    await expect(page.getByText("Administrator value", { exact: true })).toBeVisible();
  } finally {
    await deleteCollection(page, name);
  }
});

test("imports, exports and updates schema without rounding JSON defaults or dropping records", async ({ page }, info) => {
  test.setTimeout(120_000);
  const name = uniqueName("transfer", info);
  const rules = '"rules":{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}';
  const document = `{"format":"pgbase.schema/v1","collections":[{"name":"${name}","userAccess":false,${rules},"fields":[{"name":"title","type":"text","presentable":true,"help":"Imported title help"},{"name":"metadata","type":"jsonb","default":{"precise":9007199254740993}},{"name":"secret","type":"text","hidden":true,"default":"private"}]}]}`;
  try {
    await page.goto("/admin/schema-transfer");
    await expect(page.getByRole("heading", { name: "Schema import and export" })).toBeVisible();
    await page.getByLabel("Schema document").fill(document);
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(page.getByRole("button", { name: "Apply import" })).toBeVisible();
    await page.getByRole("button", { name: "Apply import" }).click();
    await expect(page.getByText(/Schema import committed at version/)).toBeVisible();

    await createRecord(page, name, { title: "Preserved record" });
    await page.goto("/admin/schema-transfer");
    await page.evaluate(() => {
      const original = URL.createObjectURL;
      URL.createObjectURL = (blob) => {
        (window as unknown as { schemaExport: Blob }).schemaExport = blob;
        return original(blob);
      };
    });
    await page.getByRole("button", { name: "Export schema" }).click();
    await expect(page.getByLabel("Schema document")).toHaveValue(new RegExp(name));
    const exported = await page.evaluate(() => (window as unknown as { schemaExport: Blob }).schemaExport.text());
    expect(exported).toContain("9007199254740993");
    expect(exported).not.toContain("9007199254740992");
    expect(exported).toContain("Imported title help");

    await page.getByLabel("Schema document").fill(`{"format":"pgbase.schema/v1","collections":[{"name":"${name}","userAccess":false,${rules},"fields":[{"name":"title","type":"text","presentable":true,"help":"Updated title help"}]}]}`);
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(page.getByRole("button", { name: "Apply import" })).toBeVisible();
    await page.getByRole("button", { name: "Apply import" }).click();
    await expect(page.getByText(/Schema import committed at version/)).toBeVisible();
    await page.goto(`/admin/collections/${name}/records`);
    await expect(page.getByText("Preserved record", { exact: true })).toBeVisible();
    await page.goto(`/admin/collections/${name}/edit`);
    await expect(page.getByLabel("Field 1 help")).toHaveValue("Updated title help");
    await expect(page.getByLabel("Field 2 name")).toHaveValue("metadata");
    await expect(page.getByLabel("Field 3 hidden")).toBeChecked();
  } finally {
    await deleteCollection(page, name);
  }
});

test("saves and reloads OTP policy and mail template in a separate realm", async ({ page }, info) => {
  const name = uniqueName("otp_policy", info);
  await page.goto("/admin/auth-collections");
  await page.getByLabel("Auth collection name").fill(name);
  await page.getByRole("button", { name: "Create auth collection", exact: true }).click();
  const realm = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name, exact: true }) });
  try {
    await expect(realm).toBeVisible();
    await realm.getByText("Authentication settings", { exact: true }).click();
    await realm.getByLabel("Email one-time-code login").check();
    await realm.getByLabel("OTP lifetime in seconds").fill("120");
    await realm.getByLabel("OTP attempts").fill("4");
    await realm.getByRole("button", { name: "Save policy", exact: true }).click();
    await expect(realm.getByText(/^Policy saved\./)).toBeVisible();
    const template = realm.getByRole("heading", { name: "otp", exact: true }).locator("..");
    await template.getByLabel("Subject", { exact: true }).fill("Your application login code");
    await template.getByLabel("Plain text alternative").fill("Use {{otp}} to sign in.");
    await template.getByRole("button", { name: "Save template" }).click();
    await expect(realm.getByText("Mail template saved.", { exact: true })).toBeVisible();
    await template.getByRole("button", { name: "Validate and preview" }).click();
    await expect(template.getByText("Use 123456 to sign in.", { exact: true })).toBeVisible();

    const provider = realm.getByRole("group", { name: "OAuth providers", exact: true });
    await provider.getByLabel("Provider preset").selectOption("google");
    await provider.getByLabel("Client ID", { exact: true }).fill("local-browser-client");
    await provider.getByLabel("Redirect URIs (one per line)").fill("https://app.example.test/callback");
    await provider.getByLabel("Avatar URL field").fill("picture");
    await provider.getByRole("button", { name: "Save provider" }).click();
    await expect(realm.getByText("OAuth provider saved.", { exact: true })).toBeVisible();
    await provider.getByRole("button", { name: "Edit", exact: true }).click();
    await expect(provider.getByLabel("Avatar URL field")).toHaveValue("picture");
    await provider.getByRole("button", { name: "Save provider" }).click();
    await expect(realm.getByText("OAuth provider saved.", { exact: true })).toBeVisible();

    const smtp = realm.getByRole("group", { name: "SMTP", exact: true });
    await smtp.getByLabel("Server address").fill("127.0.0.1:2525");
    await smtp.getByLabel("From", { exact: true }).fill("noreply@example.test");
    await smtp.getByLabel("Allow insecure local SMTP").check();
    await smtp.getByRole("button", { name: "Save SMTP", exact: true }).click();
    await expect(realm.getByText(/^SMTP settings saved\./)).toBeVisible();
    await page.reload();
    await realm.getByText("Authentication settings", { exact: true }).click();
    await expect(realm.getByLabel("Email one-time-code login")).toBeChecked();
    await expect(realm.getByLabel("OTP lifetime in seconds")).toHaveValue("120");
    await expect(realm.getByLabel("OTP attempts")).toHaveValue("4");
    await expect(template.getByLabel("Subject", { exact: true })).toHaveValue("Your application login code");
    await expect(smtp.getByLabel("Server address")).toHaveValue("127.0.0.1:2525");
    await smtp.getByRole("button", { name: "Save SMTP", exact: true }).click();
    await expect(realm.getByText(/^SMTP settings saved\./)).toBeVisible();
  } finally {
    await realm.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(realm).toHaveCount(0);
  }
});

test("creates a marked impersonation session and revokes it from user management", async ({ page }, info) => {
  const name = uniqueName("impersonate", info);
  const email = `${name}@example.test`;
  await page.goto("/admin/auth-collections");
  const setup = await page.evaluate(async ({ realmName, userEmail }) => {
    const session = await fetch("/api/admin/session").then(response => response.json());
    const realmID = crypto.randomUUID();
    const realm = await fetch("/api/admin/auth-collections", {
      method: "POST", headers: { "Content-Type": "application/json", "X-PgBase-CSRF": session.csrfToken },
      body: JSON.stringify({ id: realmID, name: realmName, registrationEnabled: true, profileSchema: {} }),
    });
    if (realm.status !== 201) return { realmStatus: realm.status, userStatus: 0 };
    const user = await fetch(`/api/v1/auth/${realmName}/register`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: userEmail, password: "Browser only correct horse battery staple" }),
    });
    return { realmStatus: realm.status, userStatus: user.status };
  }, { realmName: name, userEmail: email });
  expect(setup).toEqual({ realmStatus: 201, userStatus: 201 });
  await page.goto("/admin/users");
  const row = page.getByRole("row").filter({ hasText: email });
  await expect(row).toBeVisible();
  page.once("dialog", dialog => dialog.accept());
  await row.getByRole("button", { name: "Impersonate", exact: true }).click();
  await expect(page.getByRole("region", { name: "Impersonation session" })).toBeVisible();
  const token = await page.getByLabel("Impersonation token").inputValue();
  expect(token.length).toBeGreaterThan(20);
  const status = await page.evaluate(async ({ realmName, bearer }) => {
    const response = await fetch(`/api/v1/auth/${realmName}/session`, { headers: { Authorization: `Bearer ${bearer}` } });
    const body = await response.json();
    const mutation = await fetch("/api/v1/auth/change-password", {
      method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
      body: JSON.stringify({ currentPassword: "Browser only correct horse battery staple", newPassword: "A different browser only password" }),
    });
    return { status: response.status, impersonated: body.impersonated, hasActor: Boolean(body.impersonatingAdministratorId), mutationStatus: mutation.status };
  }, { realmName: name, bearer: token });
  expect(status).toEqual({ status: 200, impersonated: true, hasActor: true, mutationStatus: 401 });
  page.once("dialog", dialog => dialog.accept());
  await row.getByRole("button", { name: "Revoke sessions", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Revoked");
  const revoked = await page.evaluate(async ({ realmName, bearer }) => (await fetch(`/api/v1/auth/${realmName}/session`, { headers: { Authorization: `Bearer ${bearer}` } })).status, { realmName: name, bearer: token });
  expect(revoked).toBe(401);
  await page.getByRole("button", { name: "Dismiss", exact: true }).click();
  await expect(page.getByLabel("Impersonation token")).toHaveCount(0);
});

test("recovers a committed schema import after losing the apply response", async ({ page }, info) => {
  const name = uniqueName("recover_transfer", info);
  const sentinel = "Schema default must not enter recovery storage";
  const document = JSON.stringify({ format: "pgbase.schema/v1", collections: [{ name, userAccess: false, fields: [{ name: "title", type: "text", default: sentinel }], rules: { list: { op: "owner" }, view: { op: "owner" }, create: { op: "owner" }, update: { op: "owner" }, delete: { op: "owner" } } }] });
  try {
    await page.goto("/admin/schema-transfer");
    await page.getByLabel("Schema document").fill(document);
    await page.getByRole("button", { name: "Preview import" }).click();
    await expect(page.getByRole("button", { name: "Apply import" })).toBeVisible();
    await page.route("**/api/admin/schema/import/operations/*", (route) => route.abort("failed"));
    await page.route("**/api/admin/schema/import/apply", async (route) => {
      const result = await route.fetch();
      expect(result.status()).toBe(200);
      await route.abort("failed");
    });
    await page.getByRole("button", { name: "Apply import" }).click();
    await expect(page.getByText(/was not confirmed/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Preview import" })).toBeDisabled();
    const recovery = await page.evaluate(() => Object.entries(sessionStorage).filter(([key]) => key.startsWith("pgbase.schemaImportOperation")));
    expect(recovery).toHaveLength(1);
    expect(JSON.stringify(recovery)).not.toContain(sentinel);
    await page.unroute("**/api/admin/schema/import/operations/*");
    await page.reload();
    await expect(page.getByText(/Schema import committed at version/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Check import status" })).toHaveCount(0);
    await page.goto(`/admin/collections/${name}/edit`);
    await expect(page.getByLabel("Field 1 name")).toHaveValue("title");
  } finally {
    await deleteCollection(page, name);
  }
});
