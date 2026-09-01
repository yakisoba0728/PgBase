import { expect, test as base } from "@playwright/test";
import { mkdir, rm } from "node:fs/promises";
import path from "node:path";

type AuthenticatedWorkerFixtures = {
  authenticatedStorageState: string;
};

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
      await page.goto(new URL("/admin/", baseURL).href);
      await expect(page.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
      await page.getByLabel("Email").fill(email);
      await page.getByLabel("Password").fill(password);
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
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

export { expect };
