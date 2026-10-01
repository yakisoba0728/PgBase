import assert from "node:assert/strict";
import { test } from "node:test";
import { PgBaseAdminClient, PgBaseClient } from "../dist/index.js";

test("public TOTP disable uses the native factor contract and cancellation", async () => {
  const controller = new AbortController();
  let observed;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (url, init) => {
    observed = { url: String(url), init };
    return new Response(null, { status: 204 });
  }});
  await client.disableTOTP("current password", "123456", controller.signal);
  assert.equal(observed.url, "http://pgbase.test/api/v1/auth/totp/disable");
  assert.equal(observed.init.signal, controller.signal);
  assert.deepEqual(JSON.parse(observed.init.body), { currentPassword: "current password", code: "123456" });
});

test("administrator accounts, schema polling and backup archive helpers match HTTP fixtures", async () => {
  const calls = [];
  const account = { id: "administrator/id", email: "admin@example.test", active: true, createdAt: "created", updatedAt: "updated" };
  const entry = { id: "backup/id", createdAt: "created", size: 7, postgresqlMajor: 17, sha256: "hash", storageFormat: "pgbase-v1" };
  const job = { id: "job/id", operation: "restore", backupId: entry.id, status: "failed", phase: "interrupted", error: "backup operation interrupted by process restart", createdAt: "created", updatedAt: "updated" };
  const archive = new Blob([new Uint8Array([31, 139, 8, 0])], { type: "application/gzip" });
  const client = new PgBaseAdminClient("http://pgbase.test", { fetch: async (url, init) => {
    const path = new URL(url).pathname;
    calls.push({ path, url: String(url), init });
    if (path === "/api/admin/session") return Response.json({ administrator: account, expiresAt: "later", csrfToken: "csrf" });
    if (path.includes("schema/import/operations")) return Response.json({ operationId: "operation/id", status: "committed", version: 4, applied: 2 });
    if (path === "/api/admin/administrators" && !init.method) return Response.json({ items: [account], nextAfter: "next/id" });
    if (path === "/api/admin/administrators" && init.method === "POST") return Response.json(account, { status: 201 });
    if (path.includes("/administrators/") && init.method === "PATCH") return Response.json({ ...account, active: false });
    if (path.endsWith("password-reset") || path.endsWith("revoke-sessions") || init.method === "DELETE") return new Response(null, { status: 204 });
    if (path === "/api/admin/backups" && !init.method) return Response.json({ items: [entry] });
    if (path === "/api/admin/backups" && init.method === "POST") return Response.json(job, { status: 202 });
    if (path.endsWith("/backups/upload")) return Response.json(entry, { status: 201 });
    if (path.endsWith("/backups/backup%2Fid") && !init.method) return new Response(archive, { headers: { "Content-Type": "application/gzip" } });
    if (path.endsWith("/restore") || path.includes("/backup-jobs/")) return Response.json(job, { status: path.endsWith("/restore") ? 202 : 200 });
    throw new Error(`unexpected request ${path}`);
  }});
  await client.resume();
  assert.equal((await client.schemaImportOperation("operation/id")).applied, 2);
  assert.equal((await client.administratorAccounts({ limit: 20, after: "after/id" })).nextAfter, "next/id");
  await client.createAdministrator(account.email, "password");
  await client.setAdministratorActive(account.id, false);
  await client.setAdministratorPassword(account.id, "replacement");
  await client.revokeAdministratorSessions(account.id);
  assert.equal((await client.backups()).items[0].sha256, "hash");
  await client.createBackup();
  await client.uploadBackup(archive);
  assert.deepEqual(new Uint8Array(await (await client.downloadBackup(entry.id)).arrayBuffer()), new Uint8Array([31, 139, 8, 0]));
  await client.deleteBackup(entry.id);
  await client.restoreBackup(entry.id);
  assert.equal((await client.backupJob(job.id)).phase, "interrupted");

  assert.match(calls.find(call => call.path === "/api/admin/administrators" && !call.init.method).url, /limit=20&after=after%2Fid$/);
  assert.deepEqual(JSON.parse(calls.find(call => call.path.endsWith("password-reset")).init.body), { newPassword: "replacement" });
  assert.equal(calls.find(call => call.path.endsWith("revoke-sessions")).init.body, "{}");
  assert.equal(calls.find(call => call.path.endsWith("/backups/upload")).init.headers.get("Content-Type"), "application/gzip");
  assert.equal(calls.find(call => call.path.endsWith("/backups/upload")).init.body, archive);
  for (const call of calls.filter(call => call.init.method && call.path !== "/api/admin/session")) assert.equal(call.init.headers.get("X-PgBase-CSRF"), "csrf");
  assert.equal(calls.find(call => call.path.endsWith("/restore")).init.headers.has("Idempotency-Key"), false);
});
