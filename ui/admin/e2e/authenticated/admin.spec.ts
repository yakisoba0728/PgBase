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

    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/admin\/login$/);
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
    await expect(page.getByRole("navigation", { name: "Data" }).getByRole("link", { name: products })).toBeVisible();
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

test("shows request log entries", async ({ page }) => {
  await page.goto("/admin/logs");
  const rows = page.getByRole("table", { name: "Logs" }).getByRole("row");
  // The header row plus at least the requests this page itself made.
  await expect(rows.nth(1)).toBeVisible();
  await expect(rows.filter({ hasText: "/api/admin/session" }).first()).toBeVisible();
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
