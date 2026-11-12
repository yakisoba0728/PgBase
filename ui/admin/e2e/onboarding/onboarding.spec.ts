import { expect, test as base } from "@playwright/test";

// Every page must load without script errors or Content-Security-Policy violations.
const test = base.extend<{ cleanConsole: void }>({
  cleanConsole: [async ({ page }, use) => {
    const problems: string[] = [];
    await page.addInitScript(() => {
      document.addEventListener("securitypolicyviolation", (event) => console.error(`CSP violation: ${event.violatedDirective} ${event.blockedURI}`));
    });
    // HTTP 4xx answers are part of the flows (the pre-login session probe); a 5xx never is.
    page.on("console", (message) => {
      if (message.type() === "error" && !message.text().startsWith("Failed to load resource")) problems.push(message.text());
    });
    page.on("pageerror", (error) => problems.push(error.message));
    page.on("response", (response) => {
      if (response.status() >= 500) problems.push(`${response.status()} ${response.request().method()} ${response.url()}`);
    });
    await use();
    expect(problems).toEqual([]);
  }, { auto: true }],
});

test("an empty backend reaches browser setup and persists the administrator session", async ({ page }) => {
  const setupToken = process.env.PGBASE_SETUP_TOKEN;
  if (!setupToken) throw new Error("PGBASE_SETUP_TOKEN is required");

  await page.goto("/admin/");
  await expect(page).toHaveURL(/\/admin\/setup$/);
  await expect(page.getByRole("heading", { name: "First administrator" })).toBeVisible();
  await page.getByRole("textbox", { name: "Setup token" }).fill(setupToken);
  await page.getByRole("textbox", { name: "Email" }).fill("admin@example.test");
  await page.getByRole("textbox", { name: "Password", exact: true }).fill("correct horse battery staple 2026!");
  await page.getByRole("textbox", { name: "Confirm password" }).fill("correct horse battery staple 2026!");
  await page.getByRole("button", { name: "Create administrator" }).click();

  await expect(page.getByRole("heading", { name: "Collections", level: 1 })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "Collections", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Account" })).toContainText("admin@example.test");

  // Setup is one-shot: the guest route now sends the visitor to sign in instead.
  await page.context().clearCookies();
  await page.goto("/admin/setup");
  await expect(page).toHaveURL(/\/admin\/login$/);
});
