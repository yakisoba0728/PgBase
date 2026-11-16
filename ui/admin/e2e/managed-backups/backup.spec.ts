import { expect, test, type APIResponse, type Page } from "@playwright/test";
import { createHash, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

interface BackupEntry {
  id: string;
  sha256: string;
  size: number;
}

async function expectStatus(response: APIResponse, status: number): Promise<void> {
  expect(response.status(), await response.text()).toBe(status);
}

async function selectedID(page: Page, key: "backup" | "job", previous?: string): Promise<string> {
  await expect(page).toHaveURL((url) => {
    const id = url.searchParams.get(key);
    return url.pathname === "/admin/backups" && uuid.test(id ?? "") && id !== previous;
  });
  return new URL(page.url()).searchParams.get(key)!;
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
  // Observe the UI consuming each mutation receipt, then bind its selected ID to
  // authenticated API reads. Chromium can evict XHR bodies from its inspector
  // cache even when the application receives them; response.json() relies on it.
  await Promise.all([
    page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/admin/backups").then((response) => {
      expect(response.status()).toBe(202);
    }),
    page.getByRole("button", { name: "Create backup" }).click(),
  ]);
  const createJobID = await selectedID(page, "job");
  await expect(page.locator(".storage-job").getByText("Completed", { exact: true })).toBeVisible({ timeout: 90_000 });
  const jobResponse = await page.request.get(`/api/admin/backup-jobs/${createJobID}`);
  await expectStatus(jobResponse, 200);
  const job = await jobResponse.json() as { id: string; backupId: string; operation: string; status: string };
  expect(job).toMatchObject({ id: createJobID, operation: "create", status: "completed" });
  expect(job.backupId).toMatch(uuid);
  await expect(page.locator(".storage-job")).toContainText(job.backupId);
  await page.getByRole("cell", { name: job.backupId, exact: true }).click();
  // The completed job and archive details both have a "Backup" landmark.
  // Only the selected archive pane owns the actual download link.
  const pane = page.getByRole("region", { name: "Backup", exact: true }).filter({
    has: page.getByRole("link", { name: "Download", exact: true }),
  });
  await expect(pane).toHaveCount(1);
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
  const entries = await listResponse.json() as { items: BackupEntry[] };
  const entry = entries.items.find((item) => item.id === job.backupId)!;
  expect(entry).toBeDefined();
  expect(archive.length).toBe(entry.size);
  expect(createHash("sha256").update(archive).digest("hex")).toBe(entry.sha256);
  console.info("Managed backup fixture archive", { id: entry.id, size: archive.length, sha256: entry.sha256 });

  // A separately uploaded archive must restore actual contents, not merely list
  // metadata. Mutations after the snapshot must disappear from the active app.
  await Promise.all([
    page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/admin/backups/upload").then((response) => {
      expect(response.status()).toBe(201);
    }),
    page.getByLabel("Upload archive", { exact: true }).setInputFiles(archivePath),
  ]);
  await expect(page.getByRole("status").getByText("Archive uploaded.")).toBeVisible();
  const uploadedID = await selectedID(page, "backup", job.backupId);
  const uploadedListResponse = await page.request.get("/api/admin/backups");
  await expectStatus(uploadedListResponse, 200);
  const uploadedEntries = await uploadedListResponse.json() as { items: BackupEntry[] };
  const newEntries = uploadedEntries.items.filter((item) => !entries.items.some((before) => before.id === item.id));
  expect(newEntries.map((item) => item.id)).toEqual([uploadedID]);
  const uploaded = newEntries[0];
  expect(uploaded.size).toBeGreaterThan(0);
  expect(uploaded.sha256).toMatch(/^[0-9a-f]{64}$/);
  await expect(pane).toContainText(uploaded.id);
  await expect(pane).toContainText(uploaded.sha256);
  await expect(pane.getByRole("link", { name: "Download", exact: true })).toHaveAttribute("href", `/api/admin/backups/${uploaded.id}`);
  const uploadedDownloadEvent = page.waitForEvent("download");
  await pane.getByRole("link", { name: "Download", exact: true }).click();
  const uploadedDownload = await uploadedDownloadEvent;
  expect(uploadedDownload.suggestedFilename()).toBe(`pgbase-${uploaded.id}.tar.gz`);
  const uploadedArchivePath = testInfo.outputPath(uploadedDownload.suggestedFilename());
  await uploadedDownload.saveAs(uploadedArchivePath);
  const uploadedArchive = await readFile(uploadedArchivePath);
  // Upload verifies and repacks the archive, so validate its own published
  // digest rather than assuming the gzip container is byte-for-byte unchanged.
  expect(uploadedArchive.length).toBe(uploaded.size);
  expect(createHash("sha256").update(uploadedArchive).digest("hex")).toBe(uploaded.sha256);
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
  await Promise.all([
    page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/admin/backups/${uploaded.id}/restore`).then((response) => {
      expect(response.status()).toBe(202);
    }),
    confirm.click(),
  ]);
  const restoreJobID = await selectedID(page, "job", createJobID);
  await expect(page.locator(".storage-job")).toContainText(uploaded.id);
  await expect(page.locator(".storage-job").getByText("Completed", { exact: true })).toBeVisible({ timeout: 90_000 });
  await expect.poll(async () => (await page.request.get("/health/ready")).status(), { timeout: 30_000 }).toBe(200);
  const restoredJobResponse = await page.request.get(`/api/admin/backup-jobs/${restoreJobID}`);
  await expectStatus(restoredJobResponse, 200);
  expect(await restoredJobResponse.json()).toMatchObject({ id: restoreJobID, backupId: uploaded.id, operation: "restore", status: "completed" });

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
