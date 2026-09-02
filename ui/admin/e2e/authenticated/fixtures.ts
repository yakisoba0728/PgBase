import { expect, test as base, type Locator, type Page, type TestInfo } from "@playwright/test";
import { mkdir, rm } from "node:fs/promises";
import { randomBytes } from "node:crypto";
import path from "node:path";

type AuthenticatedWorkerFixtures = { authenticatedStorageState: string };

export const test = base.extend<{}, AuthenticatedWorkerFixtures>({
  storageState: ({ authenticatedStorageState }, use) => use(authenticatedStorageState),
  authenticatedStorageState: [async ({ browser }, use, workerInfo) => {
    const email = process.env.PGBASE_ADMIN_EMAIL;
    const password = process.env.PGBASE_ADMIN_PASSWORD;
    if (!email || !password) {
      throw new Error("PGBASE_ADMIN_EMAIL and PGBASE_ADMIN_PASSWORD are required for authenticated Playwright tests");
    }
    const baseURL = workerInfo.project.use.baseURL;
    if (typeof baseURL !== "string") {
      throw new Error("the authenticated Playwright project requires a baseURL");
    }

    const directory = path.join(workerInfo.project.outputDir, ".auth");
    const statePath = path.join(directory, `worker-${workerInfo.workerIndex}.json`);
    await mkdir(directory, { recursive: true });

    const page = await browser.newPage({ storageState: { cookies: [], origins: [] } });
    try {
      await signIn(page, new URL("/admin/", baseURL).href, email, password);
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
});

export async function signIn(page: Page, url: string, email: string, password: string): Promise<void> {
  await page.goto(url);
  await expect(page.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
}

/**
 * Collection names are `[a-z][a-z0-9_]{0,47}`, and every worker and retry runs
 * against the same backend, so a name is only ever owned by one attempt.
 */
export function uniqueName(prefix: string, info: TestInfo): string {
  return `${prefix}_${info.workerIndex}_${info.retry}_${randomBytes(4).toString("hex")}`;
}

/**
 * Previewing and applying is a two-step contract: another worker's apply may
 * move the catalog between the two, which the UI reports and expects to be
 * previewed again. Repeating that is exactly what an operator would do, so a
 * parallel run never has to serialise on the shared backend.
 */
async function applyPlan(page: Page, preview: string, apply: string, done: Locator): Promise<void> {
  const moved = page.getByRole("alert").filter({ hasText: "The catalog changed" });
  for (let attempt = 0; attempt < 5; attempt += 1) {
    await page.getByRole("button", { name: preview }).click();
    const applyButton = page.getByRole("button", { name: apply });
    await expect(applyButton).toBeVisible();
    await applyButton.click();
    await expect(done.or(moved).first()).toBeVisible();
    if (await done.count() > 0) return;
  }
  throw new Error(`${apply} kept losing a catalog race`);
}

export interface FieldDraft {
  name: string;
  type?: string;
  indexed?: boolean;
  relationTarget?: string;
}

export async function createCollection(page: Page, name: string, fields: FieldDraft[]): Promise<void> {
  await page.goto("/admin/collections/new");
  await expect(page.getByRole("heading", { name: "Create collection" })).toBeVisible();
  await page.getByLabel("Collection name").fill(name);
  for (const [index, field] of fields.entries()) {
    if (index > 0) await page.getByRole("button", { name: "Add field" }).click();
    await fillField(page, index, field);
  }
  await applyPlan(page, "Preview collection", "Apply collection", page.getByRole("heading", { name: "Collections" }));
}

export async function fillField(page: Page, index: number, field: FieldDraft): Promise<void> {
  await page.getByLabel(`Field ${index + 1} name`).fill(field.name);
  if (field.type !== undefined) await page.getByLabel(`Field ${index + 1} type`).selectOption(field.type);
  if (field.relationTarget !== undefined) {
    const target = page.getByLabel(`Field ${index + 1} relation target`);
    await expect(target).toBeEnabled();
    await target.selectOption(field.relationTarget);
  }
  if (field.indexed === true) await page.getByLabel(`Field ${index + 1} indexed`).check();
}

export async function applyCollectionEdit(page: Page, collection: string): Promise<void> {
  await applyPlan(page, "Preview change", "Apply change", page.getByRole("heading", { name: `${collection} records` }));
}

export async function deleteCollection(page: Page, name: string): Promise<void> {
  const listing = page.getByRole("heading", { name: "Collections" });
  const moved = page.getByRole("alert").filter({ hasText: "The catalog changed" });
  for (let attempt = 0; attempt < 5; attempt += 1) {
    await page.goto(`/admin/collections/${encodeURIComponent(name)}/edit`);
    const missing = page.getByText("Collection not found.");
    await expect(missing.or(page.getByRole("button", { name: "Delete collection" })).first()).toBeVisible();
    if (await missing.count() > 0) return;
    await page.getByRole("button", { name: "Delete collection" }).click();
    await page.getByRole("button", { name: "Confirm delete" }).click();
    await expect(listing.or(moved).first()).toBeVisible();
    if (await listing.count() > 0) return;
  }
  throw new Error(`deleting ${name} kept losing a catalog race`);
}

export async function createRecord(page: Page, collection: string, values: Record<string, string>): Promise<void> {
  await page.goto(`/admin/collections/${encodeURIComponent(collection)}/records/new`);
  await expect(page.getByRole("heading", { name: `Create ${collection} record` })).toBeVisible();
  for (const [field, value] of Object.entries(values)) {
    await page.getByLabel(`${field} state`).selectOption("value");
    await page.getByLabel(field, { exact: true }).fill(value);
  }
  await page.getByRole("button", { name: "Create record" }).click();
  await expect(page.getByRole("heading", { name: `${collection} records` })).toBeVisible();
}

export { expect };
