import { expect, test as base, type Page } from "@playwright/test";
import { randomBytes } from "node:crypto";
import { mkdir, rm } from "node:fs/promises";
import path from "node:path";

function credentials(): { email: string; password: string } {
  const email = process.env.PGBASE_ADMIN_EMAIL;
  const password = process.env.PGBASE_ADMIN_PASSWORD;
  if (!email || !password) throw new Error("PGBASE_ADMIN_EMAIL and PGBASE_ADMIN_PASSWORD are required for authenticated Playwright tests");
  return { email, password };
}

async function signIn(page: Page, email: string, password: string): Promise<void> {
  await page.goto("/admin/login");
  await page.getByRole("textbox", { name: "Email" }).fill(email);
  await page.getByRole("textbox", { name: "Password", exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

const test = base.extend<{ cleanConsole: void }, { authenticatedStorageState: string }>({
  // One sign-in per worker; every test then starts from that saved session.
  authenticatedStorageState: [async ({ browser }, use, workerInfo) => {
    const { email, password } = credentials();
    const statePath = path.join(workerInfo.project.outputDir, ".auth", `worker-${workerInfo.workerIndex}.json`);
    await mkdir(path.dirname(statePath), { recursive: true });
    const page = await browser.newPage({ baseURL: workerInfo.project.use.baseURL, storageState: { cookies: [], origins: [] } });
    try {
      await signIn(page, email, password);
      await expect(page.getByRole("button", { name: "Account" })).toContainText(email);
      await page.context().storageState({ path: statePath });
    } finally {
      await page.close();
    }
    try {
      await use(statePath);
    } finally {
      await rm(statePath, { force: true });
    }
  }, { scope: "worker" }],
  storageState: ({ authenticatedStorageState }, use) => use(authenticatedStorageState),
  // Every page must load without script errors or Content-Security-Policy violations.
  cleanConsole: [async ({ page }, use) => {
    const problems: string[] = [];
    await page.addInitScript(() => {
      document.addEventListener("securitypolicyviolation", (event) => console.error(`CSP violation: ${event.violatedDirective} ${event.blockedURI}`));
    });
    // HTTP 4xx answers are part of the flows (a rejected sign-in, the session probe); a 5xx never is.
    page.on("console", (message) => {
      if (message.type() === "error" && !message.text().startsWith("Failed to load resource")) problems.push(message.text());
    });
    page.on("pageerror", (error) => problems.push(error.message));
    const failures: Promise<void>[] = [];
    page.on("response", (response) => {
      if (response.status() < 500) return;
      failures.push(response.text().catch(() => "").then((body) => {
        problems.push(`${response.status()} ${response.request().method()} ${response.url()} ${body.slice(0, 300)}`);
      }));
    });
    await use();
    await Promise.all(failures);
    expect(problems).toEqual([]);
  }, { auto: true }],
});

// Collection names are [a-z][a-z0-9_]*; reruns against one backend must not collide.
const suffix = randomBytes(3).toString("hex");
const products = `e2e_products_${suffix}`;
const members = `e2e_members_${suffix}`;
const orders = `e2e_orders_${suffix}`;

async function addField(page: Page, type: string, name: string): Promise<void> {
  const detail = page.getByRole("region", { name: "Schema detail" });
  const previous = await detail.getByRole("heading").textContent({ timeout: 1_000 }).catch(() => null);
  await page.getByRole("button", { name: "Add field" }).click();
  // Chosen by keyboard: the 14-type menu can extend below a 720px-high viewport. The menu
  // focuses its first item when it opens, so wait for that before moving focus to the type.
  await expect(page.getByRole("menuitem").first()).toBeFocused();
  await page.getByRole("menuitem", { name: type, exact: true }).press("Enter");
  // The detail pane switches to the new field (named after its type) before it can be renamed.
  if (previous !== null) await expect(detail.getByRole("heading")).not.toHaveText(previous);
  await expect(detail.getByRole("combobox", { name: "Type" }).locator("option:checked")).toHaveText(type);
  await detail.getByRole("textbox", { name: "Name", exact: true }).fill(name);
  await expect(detail.getByRole("heading")).toHaveText(name);
}

async function reviewAndApply(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Review changes" }).click();
  const apply = page.getByRole("button", { name: "Apply", exact: true });
  await expect(apply).toBeEnabled();
  await apply.click();
  await expect(page.getByText("Schema applied.")).toBeVisible();
}

test.describe("session", () => {
  // Signing out ends the session it uses, so this starts signed out instead of from the worker session.
  test.use({ storageState: { cookies: [], origins: [] } });

  test("rejects bad credentials, restores the session after a reload, and signs out", async ({ page }) => {
    const { email, password } = credentials();
    // An unknown account keeps reruns from tripping the administrator's own sign-in throttle.
    await signIn(page, `nobody-${suffix}@example.test`, "definitely not the password");
    await expect(page.getByRole("alert")).toHaveText("Wrong email or password.");
    await expect(page).toHaveURL(/\/admin\/login$/);

    await signIn(page, email, password);
    await expect(page.getByRole("button", { name: "Account" })).toContainText(email);
    await page.reload();
    await expect(page.getByRole("button", { name: "Account" })).toContainText(email);

    // The server session ends once /login is reached, so wait for that request before probing it.
    const loggedOut = page.waitForResponse((response) => response.url().endsWith("/api/admin/auth/logout") && response.ok());
    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/admin\/login$/);
    await loggedOut;
    await page.goto("/admin/collections");
    await expect(page).toHaveURL(/\/admin\/login$/);
  });
});

test.describe.serial("collection workflow", () => {
  test("creates a base collection through the schema designer plan and apply", async ({ page }) => {
    await page.goto("/admin/new-collection");
    await page.getByRole("link", { name: /^Base/ }).click();
    await page.getByRole("region", { name: "Collection" }).getByRole("textbox", { name: "Name", exact: true }).fill(products);
    await addField(page, "Text", "title");
    await addField(page, "Integer", "qty");
    await addField(page, "Boolean", "active");
    await reviewAndApply(page);
    await expect(page).toHaveURL(new RegExp(`/admin/collections/${products}`));
    await expect(page.getByRole("navigation", { name: "Data" }).getByRole("link", { name: products, exact: true })).toBeVisible();
  });

  test("creates, edits, filters and deletes records", async ({ page }) => {
    await page.goto(`/admin/collections/${products}`);
    const detail = page.getByRole("region", { name: "Record" });
    for (const [title, qty] of [["Alpha", "1"], ["Beta", "2"], ["Gamma", "3"]]) {
      await page.getByRole("button", { name: "New record" }).click();
      await expect(detail.getByRole("heading", { name: "New record" })).toBeVisible();
      await detail.getByRole("textbox", { name: /^title/ }).fill(title);
      await detail.getByRole("textbox", { name: /^qty/ }).fill(qty);
      await detail.getByRole("button", { name: "Create", exact: true }).click();
      await expect(page).toHaveURL(/record=/);
      // The "Saved." toast sits over the pane's footer buttons until it fades, so close it like an operator would.
      await page.getByRole("status", { name: "Notifications" }).getByRole("button", { name: "Dismiss" }).click();
    }
    await expect(page.getByText("3 records")).toBeVisible();

    await detail.getByRole("textbox", { name: /^title/ }).fill("Gamma edited");
    await detail.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Saved.")).toBeVisible();
    await expect(page.getByRole("cell", { name: "Gamma edited" })).toBeVisible();

    const filter = page.getByRole("searchbox", { name: "Filter records" });
    await filter.fill("qty >= 2");
    await filter.press("Enter");
    await expect(page.getByText("2 records")).toBeVisible();
    await expect(page.getByRole("cell", { name: "Alpha" })).toHaveCount(0);

    await page.getByRole("cell", { name: "Beta" }).click();
    await detail.getByRole("button", { name: "Delete", exact: true }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Delete", exact: true }).click();
    await expect(page.getByText("1 record", { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByText("1 record", { exact: true })).toBeVisible();
  });

  test("saves an access rule", async ({ page }) => {
    await page.goto(`/admin/collections/${products}/rules`);
    await page.getByRole("checkbox", { name: "Allow requests from users and guests" }).check();
    await page.getByRole("radiogroup", { name: "Who can list" }).getByRole("radio", { name: "Everyone" }).check();
    await expect(page.getByText("Unsaved changes", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Saved", { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("checkbox", { name: "Allow requests from users and guests" })).toBeChecked();
    await expect(page.getByRole("radiogroup", { name: "Who can list" }).getByRole("radio", { name: "Everyone" })).toBeChecked();
  });

  test("picks a related record in the record editor", async ({ page }) => {
    await page.goto("/admin/new-collection");
    await page.getByRole("link", { name: /^Base/ }).click();
    await page.getByRole("region", { name: "Collection" }).getByRole("textbox", { name: "Name", exact: true }).fill(orders);
    await addField(page, "Relation", "item");
    await page.getByRole("region", { name: "Schema detail" }).getByRole("combobox", { name: "Target collection" }).selectOption(products);
    await reviewAndApply(page);

    await page.goto(`/admin/collections/${orders}`);
    await page.getByRole("button", { name: "New record" }).click();
    const detail = page.getByRole("region", { name: "Record" });
    await detail.getByRole("combobox", { name: /^item/ }).fill("Alpha");
    await detail.getByRole("option", { name: /^Alpha/ }).click();
    await expect(detail.getByRole("list", { name: /item/ })).toContainText("Alpha");
    await detail.getByRole("button", { name: "Create", exact: true }).click();
    await expect(page).toHaveURL(/record=/);
    await page.reload();
    await expect(page.getByRole("region", { name: "Record" }).getByRole("list", { name: /item/ })).toContainText("Alpha");
  });

  test("a data-losing schema change needs the collection name typed", async ({ page }) => {
    await page.goto(`/admin/collections/${products}/schema`);
    await page.getByRole("button", { name: /^active/ }).click();
    await page.getByRole("region", { name: "Schema detail" }).getByRole("button", { name: "Remove field" }).click();
    await page.getByRole("button", { name: "Review changes" }).click();
    await page.getByRole("button", { name: "Apply", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "This change loses data" });
    const confirm = dialog.getByRole("button", { name: "Apply", exact: true });
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox", { name: `Type ${products} to confirm` }).fill(products);
    await confirm.click();
    await expect(page.getByText("Schema applied.")).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: /^title/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^active/ })).toHaveCount(0);
  });
});

test.describe.serial("auth workflow", () => {
  test("creates an auth collection and saves its password policy", async ({ page }) => {
    await page.goto("/admin/new-collection/auth");
    await page.getByRole("region", { name: "Collection" }).getByRole("textbox", { name: "Name", exact: true }).fill(members);
    await reviewAndApply(page);

    await page.goto(`/admin/auth/collections/${members}`);
    const minimum = page.getByRole("textbox", { name: "Minimum length (bytes)" });
    await expect(minimum).toHaveValue("12");
    await minimum.fill("14");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("status").getByText("Saved.")).toBeVisible();
    await page.reload();
    await expect(minimum).toHaveValue("14");
  });

  test("creates a user in the auth collection", async ({ page }) => {
    await page.goto("/admin/auth/users");
    await page.getByRole("button", { name: "New user" }).click();
    const detail = page.getByRole("region", { name: "User" });
    const email = `user-${suffix}@example.test`;
    await detail.getByRole("combobox", { name: "Auth collection" }).selectOption(members);
    await detail.getByRole("textbox", { name: "Email" }).fill(email);
    await detail.getByRole("textbox", { name: "Password", exact: true }).fill(`user password ${suffix}`);
    await detail.getByRole("button", { name: "Create", exact: true }).click();
    await expect(page).toHaveURL(/user=/);
    await page.getByRole("searchbox", { name: "Search" }).fill(email);
    await expect(page.getByRole("cell", { name: email })).toBeVisible();
  });
});

test.describe.serial("administrator security", () => {
  // A throwaway administrator owns the password change and the allowlist, so the shared
  // worker session and the operator's credentials survive reruns. Wrong passwords are kept
  // to one per test: they count toward the sign-in throttle of this network.
  const admin = { email: `admin-${suffix}@example.test`, password: `admin password ${suffix}` };
  const changed = `changed password ${suffix}`;

  async function signInAs(page: Page, email: string, password: string): Promise<void> {
    await page.context().clearCookies();
    await signIn(page, email, password);
    await expect(page.getByRole("button", { name: "Account" })).toContainText(email);
  }

  test("creating an administrator requires the current password", async ({ page }) => {
    await page.goto("/admin/administrators");
    await page.getByRole("button", { name: "New administrator" }).click();
    const form = page.getByRole("region", { name: "Administrator" });
    await form.getByRole("textbox", { name: "Email" }).fill(admin.email);
    await form.getByRole("textbox", { name: "Password", exact: true }).fill(admin.password);
    const current = form.getByRole("textbox", { name: "Current password" });
    await current.fill("not the current password");
    await form.getByRole("button", { name: "Create", exact: true }).click();
    await expect(form.getByText("The current password is incorrect.")).toBeVisible();
    await expect(page.getByRole("cell", { name: admin.email })).toHaveCount(0);

    await current.fill(credentials().password);
    await form.getByRole("button", { name: "Create", exact: true }).click();
    await expect(page.getByRole("status").getByText(`${admin.email} added.`)).toBeVisible();
    await expect(page.getByRole("cell", { name: admin.email })).toBeVisible();
  });

  test("changing the profile password requires the current password", async ({ page }) => {
    await signInAs(page, admin.email, admin.password);
    await page.goto("/admin/profile");
    const current = page.getByRole("textbox", { name: "Current password" }).nth(1);
    await page.getByRole("textbox", { name: "New password" }).fill(changed);
    await page.getByRole("textbox", { name: "Repeat password" }).fill(changed);
    await current.fill("not the current password");
    await page.getByRole("button", { name: "Set password" }).click();
    await expect(page.getByText("The current password is incorrect.")).toBeVisible();

    // The change ends every session of the account, this one included.
    await current.fill(admin.password);
    await page.getByRole("button", { name: "Set password" }).click();
    await expect(page).toHaveURL(/\/admin\/login$/);
    await signIn(page, admin.email, changed);
    await expect(page.getByRole("button", { name: "Account" })).toContainText(admin.email);
  });

  test("the IP allowlist refuses a list that excludes the caller", async ({ page, baseURL }) => {
    await signInAs(page, admin.email, changed);
    await page.goto("/admin/profile");
    const networks = page.getByRole("textbox", { name: "Networks" });
    // The address the server sees: loopback here, the Docker gateway behind the Compose port mapping.
    const { clientAddress } = await (await page.request.get("/api/admin/profile/ip-allowlist")).json() as { clientAddress: string };
    await networks.fill("198.51.100.0/24");
    await expect(page.getByText(`Your current address ${clientAddress} is not inside this list, so it cannot be saved.`)).toBeVisible();
    await expect(page.locator("form").filter({ has: networks }).getByRole("button", { name: "Save", exact: true })).toBeDisabled();

    // The page only mirrors the rule; the server is what refuses it.
    const session = await (await page.request.get("/api/admin/session")).json() as { csrfToken: string };
    const refused = await page.request.patch("/api/admin/profile/ip-allowlist", {
      headers: { "X-PgBase-CSRF": session.csrfToken, Origin: new URL(baseURL ?? "").origin },
      data: { currentPassword: changed, prefixes: ["198.51.100.0/24"] },
    });
    expect(refused.status()).toBe(409);
    expect(await refused.json()).toMatchObject({ error: { code: "ip_allowlist_excludes_caller" } });
    const after = await (await page.request.get("/api/admin/profile/ip-allowlist")).json() as { prefixes: string[] };
    expect(after.prefixes).toEqual([]);
  });
});

test("shows request log entries", async ({ page }) => {
  await page.goto("/admin/logs");
  const rows = page.getByRole("table", { name: "Logs" }).getByRole("row");
  // The header row plus at least one entry: earlier tests and this page's own requests are logged.
  await expect(rows.nth(1)).toBeVisible();
});

test("saves application settings", async ({ page }) => {
  await page.goto("/admin/settings");
  const retention = page.getByRole("textbox", { name: "Retention (days)" });
  await expect(retention).not.toHaveValue("");
  const next = (await retention.inputValue()) === "8" ? "9" : "8";
  await retention.fill(next);
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("status").getByText("Settings saved.")).toBeVisible();
  await page.reload();
  await expect(retention).toHaveValue(next);
});

test("creates a managed backup", async ({ page }) => {
  // The CI image has no pg_dump and no backup directory, so this needs a managed-host fixture.
  test.skip(process.env.PGBASE_BROWSER_MANAGED_BACKUPS !== "1", "requires a disposable managed-host fixture");
  test.setTimeout(120_000);
  await page.goto("/admin/backups");
  await page.getByRole("button", { name: "Create backup" }).click();
  await expect(page.getByText("Completed").first()).toBeVisible({ timeout: 90_000 });
  await page.reload();
  await expect(page.getByText("No backups yet.")).toHaveCount(0);
});

test("switches the interface to Korean", async ({ page }) => {
  await page.goto("/admin/settings");
  await page.getByRole("button", { name: "Language" }).click();
  await page.getByRole("menuitemradio", { name: "한국어" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "ko");
  await expect(page.getByRole("heading", { name: "설정", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "인증 컬렉션" })).toBeVisible();
  // The choice is stored per browser, so it survives a reload.
  await page.reload();
  await expect(page.getByRole("heading", { name: "설정", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "언어" }).click();
  await page.getByRole("menuitemradio", { name: "English" }).click();
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
});

test("the command palette jumps to a screen", async ({ page }) => {
  await page.goto("/admin/settings");
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
  await page.keyboard.press("ControlOrMeta+k");
  const search = page.getByRole("dialog").getByRole("combobox", { name: "Go to or run a command" });
  await expect(search).toBeFocused();
  await search.fill("Logs");
  await search.press("Enter");
  await expect(page).toHaveURL(/\/admin\/logs(\?|$)/);
  await expect(page.getByRole("heading", { name: "Logs", level: 1 })).toBeVisible();
});

test("every navigation entry opens its screen", async ({ page }) => {
  await page.goto("/admin/settings");
  const nav = page.getByRole("complementary", { name: "Main" });
  // Collection links are covered above and grow with every rerun, so the tour takes the fixed screens.
  const links = (await nav.getByRole("navigation").getByRole("link").evaluateAll((anchors) =>
    anchors.map((anchor) => [anchor.textContent?.trim() ?? "", anchor.getAttribute("href") ?? ""])))
    .filter(([, href]) => !href.startsWith("/admin/collections/"));
  expect(links.length).toBeGreaterThanOrEqual(12);
  for (const [name, href] of links) {
    await nav.getByRole("link", { name, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${href}(/|\\?|$)`));
    await expect(page.getByRole("main").getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Page not found" })).toHaveCount(0);
  }
});
