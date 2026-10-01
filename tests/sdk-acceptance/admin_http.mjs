import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { PgBaseAdminClient } from "../../sdk/typescript/dist/admin.js";

const baseURL = process.env.PGBASE_ACCEPTANCE_URL;
const email = process.env.PGBASE_ACCEPTANCE_EMAIL;
const password = process.env.PGBASE_ACCEPTANCE_PASSWORD;
const archivePath = process.env.PGBASE_ACCEPTANCE_ARCHIVE;
const origin = process.env.PGBASE_ACCEPTANCE_ORIGIN;
if (!baseURL || !email || !password || !archivePath || !origin) throw new Error("missing acceptance environment");

let cookie = "";
const browserFetch = async (input, init = {}) => {
  const headers = new Headers(init.headers);
  headers.set("Origin", origin);
  if (cookie) headers.set("Cookie", cookie);
  const response = await fetch(input, { ...init, headers });
  const setCookie = response.headers.get("set-cookie");
  if (setCookie) cookie = setCookie.split(";", 1)[0];
  return response;
};

const client = new PgBaseAdminClient(baseURL, { fetch: browserFetch });
await client.login(email, password);

const accountEmail = `sdk-${crypto.randomUUID()}@example.test`;
const account = await client.createAdministrator(accountEmail, `${password}A1!`);
assert.equal(account.email, accountEmail);
assert.equal((await client.administratorAccounts({ limit: 100 })).items.some(item => item.id === account.id), true);
assert.equal((await client.setAdministratorActive(account.id, false)).active, false);
await client.setAdministratorPassword(account.id, `${password}B2!`);
await client.revokeAdministratorSessions(account.id);

const exported = await client.exportSchema();
const approved = await client.previewSchemaImport(exported);
const applied = await client.applySchemaImport(approved);
const importOperation = await client.schemaImportOperation(applied.operationId);
assert.equal(importOperation.operationId, applied.operationId);
assert.equal(importOperation.status, "committed");

const createJob = await client.createBackup();
assert.equal(createJob.operation, "create");
let createReceipt = createJob;
const createDeadline = Date.now() + 20_000;
while (createReceipt.status === "running" && Date.now() < createDeadline) {
  await new Promise(resolve => setTimeout(resolve, 50));
  createReceipt = await client.backupJob(createJob.id);
}
assert.equal(createReceipt.status, "completed", `backup creation ended as ${createReceipt.status}: ${createReceipt.error ?? ""}`);
assert.equal((await client.backups()).items.some(item => item.id === createJob.backupId), true);
await client.deleteBackup(createJob.backupId);

const archive = await readFile(archivePath);
const uploaded = await client.uploadBackup(new Blob([archive], { type: "application/gzip" }));
const downloaded = new Uint8Array(await (await client.downloadBackup(uploaded.id)).arrayBuffer());
assert.deepEqual(Buffer.from(downloaded), archive);
assert.equal((await client.backups()).items.some(item => item.id === uploaded.id), true);
await client.deleteBackup(uploaded.id);
assert.equal((await client.backups()).items.some(item => item.id === uploaded.id), false);

await client.logout();
process.stdout.write("sdk-admin-http-acceptance: ok\n");
