import { afterEach, describe, expect, it, vi } from "vitest";
import { CreateAttempt, RecordError, SchemaError, SessionError, type AdminBackend, type CollectionChange, type CollectionDefinition } from "./ports";
import { backendScript, callsTo, type BackendScript } from "../test/backend-script";
import { httpHarness } from "../test/http-harness";
import { memoryBackend } from "../test/memory-backend";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };
const expiresAt = "2026-09-02T00:00:00Z";
const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";

const posts: CollectionDefinition = {
  id: "posts-id",
  name: "posts",
  tableName: "posts",
  fields: [{ name: "title", type: "text", required: false, unique: false, indexed: true, ordinal: 0 }],
};

interface Harness { name: string; open(script: BackendScript): AdminBackend }

const harnesses: Harness[] = [
  { name: "HTTP adapter", open: (script) => httpHarness(script).backend },
  { name: "scripted-memory adapter", open: (script) => memoryBackend(script) },
];

afterEach(() => vi.unstubAllGlobals());

describe.each(harnesses)("$name conformance", ({ open }) => {
  it("resumes an authenticated administrator", async () => {
    const script = backendScript();
    script.resume.push({ value: { kind: "authenticated", session: { administrator, expiresAt } } });

    await expect(open(script).session.resume()).resolves.toEqual({
      kind: "authenticated",
      session: { administrator, expiresAt },
    });
  });

  it("resumes an unset instance as setup required", async () => {
    const script = backendScript();
    script.resume.push({ value: { kind: "setup-required" } });

    await expect(open(script).session.resume()).resolves.toEqual({ kind: "setup-required" });
  });

  it.each(["anonymous", "setup-unknown", "unreachable"] as const)("resumes a signed-out administrator as %s", async (kind) => {
    const script = backendScript();
    script.resume.push({ value: { kind } });

    await expect(open(script).session.resume()).resolves.toEqual({ kind });
  });

  it("cancels a resume that was aborted before it started", async () => {
    const script = backendScript();
    script.resume.push({ value: { kind: "anonymous" } });
    const controller = new AbortController();
    controller.abort();

    await expect(open(script).session.resume(controller.signal)).rejects.toBeDefined();
  });

  it("rejects a bad setup token and bad credentials with the invalid-credentials reason", async () => {
    const script = backendScript();
    script.setup.push({ fault: { kind: "invalid-credentials" } });
    script.login.push({ fault: { kind: "invalid-credentials" } });
    const backend = open(script);

    await expect(backend.session.setup({ setupToken: "bad", email: "a@b.test", password: "x".repeat(12) }))
      .rejects.toMatchObject({ reason: "invalid-credentials" });
    await expect(backend.session.login({ email: "a@b.test", password: "x" }))
      .rejects.toBeInstanceOf(SessionError);
  });

  it.each(["setup-unavailable", "invalid-input"] as const)("reports a %s setup failure", async (kind) => {
    const script = backendScript();
    script.setup.push({ fault: { kind } });

    await expect(open(script).session.setup({ setupToken: "t", email: "a@b.test", password: "x".repeat(12) }))
      .rejects.toMatchObject({ reason: kind });
  });

  it("announces the session expiry for a protected request the backend rejects", async () => {
    const script = backendScript();
    script.catalog.push({ fault: { kind: "unauthorized" } });
    const backend = open(script);
    const expired = vi.fn();
    backend.session.onExpired(expired);

    await expect(backend.schema.catalog()).rejects.toBeDefined();
    expect(expired).toHaveBeenCalledOnce();
  });

  it("reads a catalog that resolves collections by name and by id", async () => {
    const script = backendScript();
    script.catalog.push({ value: { version: 7, collections: [posts] } });

    const catalog = await open(script).schema.catalog();
    expect(catalog.version).toBe(7);
    expect(catalog.get("posts")).toMatchObject({ name: "posts" });
    expect(catalog.byID("posts-id")).toMatchObject({ name: "posts" });
    expect(catalog.get("missing")).toBeUndefined();
  });

  it("returns a plan that reports its preview and recognises its own input", async () => {
    const script = backendScript();
    const change: CollectionChange = { operation: "create", name: "posts", fields: [] };
    script.plan.push({ value: { baseVersion: 7, checksum: "abc", fields: [{ name: "title", type: "text", required: false, unique: false, indexed: true }], sql: ["CREATE TABLE posts"] } });

    const plan = await open(script).schema.plan(change);
    expect(plan.baseVersion).toBe(7);
    expect(plan.sql).toEqual(["CREATE TABLE posts"]);
    expect(plan.fields[0]).toMatchObject({ name: "title" });
    expect(plan.matches(change)).toBe(true);
    expect(plan.matches({ ...change, name: "other" })).toBe(false);
  });

  it("applies the approved plan under its own operation, name, checksum and base version", async () => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.apply.push({ value: undefined });
    const backend = open(script);

    await backend.schema.apply(await backend.schema.plan({ operation: "update", name: "posts", fields: [] }));

    const [applied] = callsTo(script, "apply");
    const request = "request" in applied.detail ? applied.detail.request : JSON.parse(String(applied.detail.body));
    expect(request).toEqual({ operation: "update", name: "posts", fields: [], checksum: "approved", baseVersion: 7 });
  });

  it.each(["same-version-conflict", "catalog-moved", "data-blocked"] as const)("reports an apply that failed as %s", async (kind) => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.apply.push({ fault: { kind } });
    // Only the HTTP adapter re-reads the catalog to separate the two conflicts.
    script.catalog.push({ value: { version: kind === "catalog-moved" ? 8 : 7, collections: [posts] } });
    const backend = open(script);

    const plan = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await expect(backend.schema.apply(plan)).rejects.toMatchObject({ reason: kind });
  });

  it("reports a deletion the backend refuses because the collection is referenced", async () => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    script.apply.push({ fault: { kind: "referenced", details: { name: ["collection_referenced"] } } });
    const backend = open(script);

    const plan = await backend.schema.plan({ operation: "delete", name: "posts" });
    await expect(backend.schema.apply(plan)).rejects.toMatchObject({
      reason: "referenced",
      code: "collection_referenced",
      details: { name: ["collection_referenced"] },
    });
    expect(callsTo(script, "catalog")).toHaveLength(0);
  });

  it("fails a schema read with a typed error", async () => {
    const script = backendScript();
    script.catalog.push({ fault: { kind: "unreachable" } });

    await expect(open(script).schema.catalog()).rejects.toBeInstanceOf(SchemaError);
  });

  it("carries cursor, filter and sort into a record list", async () => {
    const script = backendScript();
    script.list.push({ value: { items: [{ id: recordID }], nextCursor: "next-page" } });

    const page = await open(script).records.list("posts", { cursor: "c1", filter: "title = 'x'", sort: "-created_at" });
    expect(page).toEqual({ items: [{ id: recordID }], nextCursor: "next-page" });
    expect(callsTo(script, "list")[0].detail).toEqual({ collection: "posts", cursor: "c1", filter: "title = 'x'", sort: "-created_at" });
  });

  it("reports a filter the backend rejects as a rejected record request", async () => {
    const script = backendScript();
    script.list.push({ fault: { kind: "rejected", details: { filter: ["invalid"] } } });

    await expect(open(script).records.list("posts", { filter: "bad" }))
      .rejects.toMatchObject({ reason: "rejected", details: { filter: ["invalid"] } });
  });

  it("reads a record as a version that carries its own collection and id", async () => {
    const script = backendScript();
    script.read.push({ value: { data: { id: recordID, title: "First" }, etag: '"pgb1.one"' } });

    const record = await open(script).records.get("posts", recordID);
    expect(record.collection).toBe("posts");
    expect(record.id).toBe(recordID);
    expect(record.data).toMatchObject({ id: recordID, title: "First" });
  });

  it("reports a missing record as not found", async () => {
    const script = backendScript();
    script.read.push({ fault: { kind: "not-found" } });

    await expect(open(script).records.get("posts", recordID)).rejects.toMatchObject({ reason: "not-found" });
  });

  it("creates a record under the attempt's own collection", async () => {
    const script = backendScript();
    script.create.push({ value: { data: { id: recordID, title: "First" }, etag: '"pgb1.new"' } });

    const created = await open(script).records.create(new CreateAttempt("posts", () => "key-1"), { title: "First" });
    expect(created.collection).toBe("posts");
    expect(callsTo(script, "create")[0].detail.collection).toBe("posts");
  });

  it("patches only what the caller passed and returns the next version", async () => {
    const script = backendScript();
    script.read.push({ value: { data: { id: recordID, title: "First" }, etag: '"pgb1.one"' } });
    script.update.push({ value: { data: { id: recordID, title: "Second" }, etag: '"pgb1.two"' } });
    const backend = open(script);

    const updated = await backend.records.update(await backend.records.get("posts", recordID), { title: "Second" });
    expect(updated.data).toMatchObject({ title: "Second" });
    const patched = callsTo(script, "update")[0].detail;
    const body = "patch" in patched ? patched.patch : JSON.parse(String(patched.body));
    expect(body).toEqual({ title: "Second" });
  });

  it("reports a record another writer changed as stale", async () => {
    const script = backendScript();
    script.read.push({ value: { data: { id: recordID }, etag: '"pgb1.one"' } });
    script.update.push({ fault: { kind: "stale" } });
    const backend = open(script);

    const record = await backend.records.get("posts", recordID);
    await expect(backend.records.update(record, { title: "x" })).rejects.toMatchObject({ reason: "stale" });
  });

  it("reports a delete the backend refused with the offending field codes", async () => {
    const script = backendScript();
    script.read.push({ value: { data: { id: recordID }, etag: '"pgb1.one"' } });
    script.remove.push({ fault: { kind: "rejected", details: { id: ["relation_delete_restricted"] } } });
    const backend = open(script);

    const record = await backend.records.get("posts", recordID);
    await expect(backend.records.remove(record)).rejects.toMatchObject({
      reason: "rejected",
      details: { id: ["relation_delete_restricted"] },
    });
  });

  it("deletes a record and resolves without a value", async () => {
    const script = backendScript();
    script.read.push({ value: { data: { id: recordID }, etag: '"pgb1.one"' } });
    script.remove.push({ value: undefined });
    const backend = open(script);

    await expect(backend.records.remove(await backend.records.get("posts", recordID))).resolves.toBeUndefined();
  });

  it("reads the operational system status", async () => {
    const script = backendScript();
    const status = { version: "v1.2.3", databaseReachable: true, migrationCurrent: 4, migrationLatest: 5, migrationDirty: true, catalogVersion: 9, listenerReady: false };
    script.status.push({ value: status });

    await expect(open(script).system.status()).resolves.toEqual(status);
  });

  it("fails a system read rather than reporting a partial status", async () => {
    const script = backendScript();
    script.status.push({ fault: { kind: "unreachable" } });

    await expect(open(script).system.status()).rejects.toBeDefined();
  });

  it("keeps a record failure distinct from a schema failure", async () => {
    const script = backendScript();
    script.read.push({ fault: { kind: "stale" } });

    await expect(open(script).records.get("posts", recordID)).rejects.toBeInstanceOf(RecordError);
  });
});
