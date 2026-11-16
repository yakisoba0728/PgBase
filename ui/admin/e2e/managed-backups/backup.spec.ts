import { expect, test, type APIResponse, type Page } from "@playwright/test";
import { createHash, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";

async function expectStatus(response: APIResponse, status: number): Promise<void> {
  expect(response.status(), await response.text()).toBe(status);
}

async function signIn(page: Page): Promise<void> {
  const email = process.env.PGBASE_ADMIN_EMAIL;
  const password = process.env.PGBASE_ADMIN_PASSWORD;
  if (!email || !password) throw new Error("The isolated backup fixture requires disposable administrator credentials");
  await page.goto("/admin/login");
  await page.getByRole("textbox", { name: "Email" }).fill(email);
  await page.getByRole("textbox", { name: "Password", exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: "Account" })).toContainText(email);
}

test("creates, downloads, uploads and restores an archive with exact record and file content", async ({ page, baseURL }, testInfo) => {
  test.setTimeout(180_000);
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await signIn(page);
  const sessionResponse = await page.request.get("/api/admin/session");
  await expectStatus(sessionResponse, 200);
  const session = await sessionResponse.json() as { csrfToken: string };
  const headers = { Origin: baseURL!, "X-PgBase-CSRF": session.csrfToken };
  const collection = `restore_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
  const definition = {
    operation: "create", name: collection,
    fields: [
      { name: "title", type: "text", required: true, unique: false, indexed: false },
      { name: "attachment", type: "file", required: false, unique: false, indexed: false },
    ],
  };
  const planResponse = await page.request.post("/api/admin/collections/plan", { headers, data: definition });
  await expectStatus(planResponse, 200);
  const plan = await planResponse.json() as { baseVersion: number; checksum: string };
  const apply = await page.request.post("/api/admin/collections/apply", {
    headers: { ...headers, "PgBase-Operation-ID": randomUUID(), "Idempotency-Key": randomUUID() },
    data: { ...definition, baseVersion: plan.baseVersion, checksum: plan.checksum },
  });
  await expectStatus(apply, 201);
  const recordsPath = `/api/v1/collections/${collection}/records`;
  const created = await page.request.post(recordsPath, { headers, data: { title: "Snapshot value" } });
  await expectStatus(created, 201);
  const record = await created.json() as { id: string };
  const recordPath = `${recordsPath}/${record.id}`;
  const fileBytes = Buffer.from("Exact snapshot attachment\n한글\n");
  const uploadedFile = await page.request.post(`/api/admin/collections/${collection}/records/${record.id}/files/attachment`, {
    headers: { ...headers, "X-PgBase-File-Size": String(fileBytes.length), "X-PgBase-File-SHA256": createHash("sha256").update(fileBytes).digest("hex") },
    multipart: { file: { name: "snapshot.txt", mimeType: "text/plain", buffer: fileBytes } },
  });
  await expectStatus(uploadedFile, 201);
  const storedFile = await uploadedFile.json() as { id: string };
  const fileTokenResponse = await page.request.post(`/api/admin/files/${storedFile.id}/token`, { headers, data: { variant: "original" } });
  await expectStatus(fileTokenResponse, 200);
  const fileToken = (await fileTokenResponse.json() as { token: string }).token;
  const fileURL = `/api/admin/files/${storedFile.id}?token=${encodeURIComponent(fileToken)}`;
  const snapshotRecord = await page.request.get(recordPath);
  await expectStatus(snapshotRecord, 200);
  const snapshotEtag = snapshotRecord.headers().etag;

  await page.goto("/admin/backups");
  const createResponse = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/admin/backups");
  await page.getByRole("button", { name: "Create backup" }).click();
  const creation = await createResponse;
  expect(creation.status()).toBe(202);
  const job = await creation.json() as { backupId: string };
  await expect(page.getByText("Completed", { exact: true }).first()).toBeVisible({ timeout: 90_000 });
  await page.getByRole("cell", { name: job.backupId, exact: true }).click();
  const pane = page.getByRole("region", { name: "Backup", exact: true });
  const downloadEvent = page.waitForEvent("download");
  await pane.getByRole("link", { name: "Download", exact: true }).click();
  const download = await downloadEvent;
  expect(download.suggestedFilename()).toBe(`pgbase-${job.backupId}.tar.gz`);
  const archivePath = testInfo.outputPath(download.suggestedFilename());
  await download.saveAs(archivePath);
  const archive = await readFile(archivePath);
  expect(archive.length).toBeGreaterThan(0);
  const listResponse = await page.request.get("/api/admin/backups");
  await expectStatus(listResponse, 200);
  const entries = await listResponse.json() as { items: { id: string; sha256: string; size: number }[] };
  const entry = entries.items.find((item) => item.id === job.backupId)!;
  expect(entry).toBeDefined();
  expect(archive.length).toBe(entry.size);
  expect(createHash("sha256").update(archive).digest("hex")).toBe(entry.sha256);

  // A separately uploaded archive must restore actual contents, not merely list
  // metadata. Mutations after the snapshot must disappear from the active app.
  const uploadResponse = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/admin/backups/upload");
  await page.getByLabel("Upload archive", { exact: true }).setInputFiles(archivePath);
  const upload = await uploadResponse;
  expect(upload.status()).toBe(201);
  const uploaded = await upload.json() as { id: string };
  expect(uploaded.id).not.toBe(job.backupId);
  await expect(page.getByRole("status").getByText("Archive uploaded.")).toBeVisible();
  const changed = await page.request.patch(recordPath, { headers, data: { title: "After snapshot" } });
  await expectStatus(changed, 200);
  const after = await page.request.post(recordsPath, { headers, data: { title: "Must disappear" } });
  await expectStatus(after, 201);
  const removed = await page.request.delete(`/api/admin/files/${storedFile.id}`, { headers });
  await expectStatus(removed, 204);
  await expectStatus(await page.request.get(fileURL, { headers }), 404);

  await page.goto(`/admin/backups?backup=${uploaded.id}`);
  await pane.getByRole("button", { name: "Restore", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Restore this backup?" });
  const confirm = dialog.getByRole("button", { name: "Restore", exact: true });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole("textbox", { name: /to confirm/ }).fill(`pgbase-${uploaded.id}.tar.gz`);
  await expect(confirm).toBeEnabled();
  const restoreResponse = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/admin/backups/${uploaded.id}/restore`);
  await confirm.click();
  const restoration = await restoreResponse;
  expect(restoration.status()).toBe(202);
  const restoredJob = await restoration.json() as { id: string };
  await expect(page).toHaveURL(new RegExp(`[?&]job=${restoredJob.id}(?:&|$)`));
  await expect(page.locator(".storage-job")).toContainText(uploaded.id);
  await expect(page.locator(".storage-job").getByText("Completed", { exact: true })).toBeVisible({ timeout: 90_000 });
  await expect.poll(async () => (await page.request.get("/health/ready")).status(), { timeout: 30_000 }).toBe(200);

  const restored = await page.request.get(recordPath);
  await expectStatus(restored, 200);
  expect(await restored.json()).toMatchObject({ id: record.id, title: "Snapshot value" });
  expect(restored.headers().etag).toBe(snapshotEtag);
  const restoredList = await page.request.get(recordsPath);
  await expectStatus(restoredList, 200);
  expect((await restoredList.json() as { items: { id: string }[] }).items.map((item) => item.id)).toEqual([record.id]);
  const restoredFile = await page.request.get(fileURL, { headers });
  await expectStatus(restoredFile, 200);
  expect(await restoredFile.body()).toEqual(fileBytes);
  expect(restoredFile.headers()["content-type"]).toMatch(/^text\/plain/);
  await page.goto(`/admin/collections/${collection}`);
  await expect(page.getByRole("cell", { name: "Snapshot value", exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: "After snapshot", exact: true })).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});
