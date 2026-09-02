import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApplyAttempt,
  CreateAttempt,
  RecordError,
  SchemaError,
  SessionError,
  type AdminBackend,
  type CollectionChange,
  type CollectionDefinition,
  type OperationReceipt,
} from "./ports";
import { backendScript, callsTo, type BackendScript, type Call } from "../test/backend-script";
import { httpHarness } from "../test/http-harness";
import { memoryBackend } from "../test/memory-backend";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };
const expiresAt = "2026-09-02T00:00:00Z";
const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";

const operationID = "33333333-3333-4333-8333-333333333333";
const secondOperationID = "55555555-5555-4555-8555-555555555555";

// Deterministic identities, so a vector can name the operation an adapter must
// have sent rather than only observing that it sent one.
function mintedIdentity(...ids: string[]): () => string {
  const minted = ids.length > 0 ? [...ids] : [operationID, "44444444-4444-4444-8444-444444444444"];
  return () => minted.shift() ?? "exhausted";
}

// Both adapters record the identity they sent: the HTTP harness reads it back
// off the request headers, the scripted adapter off the call itself.
function identityOf(call: Call): Record<string, unknown> {
  return { operationId: call.detail.operationId, idempotencyKey: call.detail.idempotencyKey };
}

function scriptedReceipt(patch: Partial<OperationReceipt> = {}): OperationReceipt {
  return {
    operationId: operationID, operation: "collection_create", collectionId: "posts-id",
    status: "committed", baseVersion: 7, committedVersion: 8,
    createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:30Z", ...patch,
  };
}

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

    await backend.schema.apply(await backend.schema.plan({ operation: "update", name: "posts", fields: [] }), new ApplyAttempt(mintedIdentity()));

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
    await expect(backend.schema.apply(plan, new ApplyAttempt(mintedIdentity()))).rejects.toMatchObject({ reason: kind });
  });

  it("reports a deletion the backend refuses because the collection is referenced", async () => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    script.apply.push({ fault: { kind: "referenced", details: { name: ["collection_referenced"] } } });
    const backend = open(script);

    const plan = await backend.schema.plan({ operation: "delete", name: "posts" });
    await expect(backend.schema.apply(plan, new ApplyAttempt(mintedIdentity()))).rejects.toMatchObject({
      reason: "referenced",
      code: "collection_referenced",
      details: { name: ["collection_referenced"] },
    });
    expect(callsTo(script, "catalog")).toHaveLength(0);
  });

  it("sends one operation identity for a plan and a new one only when the plan changes", async () => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.plan.push({ value: { baseVersion: 8, checksum: "reapproved", fields: [], sql: [] } });
    script.apply.push({ value: undefined }, { value: undefined }, { value: undefined });
    const backend = open(script);
    const attempt = new ApplyAttempt(mintedIdentity(operationID, "key-one", secondOperationID, "key-two"));

    const first = await backend.schema.plan({ operation: "update", name: "posts", fields: [] });
    await backend.schema.apply(first, attempt);
    // The same approved plan retried after a lost answer must replay rather
    // than start a second operation.
    await backend.schema.apply(first, attempt);
    await backend.schema.apply(await backend.schema.plan({ operation: "update", name: "posts", fields: [] }), attempt);

    const sent = callsTo(script, "apply").map((call) => identityOf(call));
    expect(sent[0]).toEqual({ operationId: operationID, idempotencyKey: "key-one" });
    expect(sent[1]).toEqual(sent[0]);
    expect(sent[2]).toEqual({ operationId: secondOperationID, idempotencyKey: "key-two" });
  });

  it.each(["running", "unknown"] as const)("reports an apply the server left %s as undecided rather than applied", async (status) => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.apply.push({ value: { status } });
    const backend = open(script);

    const plan = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await expect(backend.schema.apply(plan, new ApplyAttempt(mintedIdentity())))
      .resolves.toEqual({ kind: "undecided", operationId: operationID });
  });

  it("reads a replayed apply as applied only when the stored receipt committed", async () => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.apply.push({ value: { status: "committed", replayed: true } });
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.apply.push({ value: { status: "running", replayed: true } });
    const backend = open(script);

    const first = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await expect(backend.schema.apply(first, new ApplyAttempt(mintedIdentity()))).resolves.toEqual({ kind: "applied" });
    const second = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await expect(backend.schema.apply(second, new ApplyAttempt(mintedIdentity()))).resolves.toMatchObject({ kind: "undecided" });
  });

  it("rejects an identity already bound to a different request without re-reading the catalog", async () => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.apply.push({ fault: { kind: "operation-conflict" } });
    const backend = open(script);

    const plan = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await expect(backend.schema.apply(plan, new ApplyAttempt(mintedIdentity())))
      .rejects.toMatchObject({ reason: "operation-conflict", code: "schema_operation_conflict" });
    expect(callsTo(script, "catalog")).toHaveLength(0);
  });

  it("reports a terminal operation failure with the bounded classification it carried", async () => {
    const script = backendScript();
    script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [], sql: [] } });
    script.apply.push({ fault: { kind: "operation-failed", details: { operation: ["apply_failed"] } } });
    const backend = open(script);

    const plan = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await expect(backend.schema.apply(plan, new ApplyAttempt(mintedIdentity()))).rejects.toMatchObject({
      reason: "operation-failed",
      code: "schema_operation_failed",
      details: { operation: ["apply_failed"] },
    });
    expect(callsTo(script, "catalog")).toHaveLength(0);
  });

  it("reads one operation receipt by the identity the apply was sent under", async () => {
    const script = backendScript();
    script.operation.push({ value: scriptedReceipt({ status: "committed" }) });

    await expect(open(script).schema.operation(operationID)).resolves.toMatchObject({
      operationId: operationID, status: "committed", committedVersion: 8,
    });
    expect(callsTo(script, "operation")[0].detail).toEqual({ operationId: operationID });
  });

  it("answers a tampered or another administrator's operation id with the same not-found reason", async () => {
    const script = backendScript();
    script.operation.push({ fault: { kind: "not-found" } });

    await expect(open(script).schema.operation(operationID))
      .rejects.toMatchObject({ reason: "not-found" });
  });

  it("reads a bounded history page in the order the ledger returned it", async () => {
    const script = backendScript();
    // Two receipts sharing a creation instant: only the ledger's own tie-break
    // orders them, so an adapter that re-sorted would reverse this pair.
    const items = [
      scriptedReceipt({ operationId: secondOperationID, createdAt: "2026-09-01T00:00:00Z" }),
      scriptedReceipt({ operationId: operationID, createdAt: "2026-09-01T00:00:00Z" }),
    ];
    script.operations.push({ value: { items, nextCursor: "page-2" } });

    const page = await open(script).schema.operations();
    expect(page.items.map((item) => item.operationId)).toEqual([secondOperationID, operationID]);
    expect(page.nextCursor).toBe("page-2");
  });

  it("carries the opaque cursor into the next history page", async () => {
    const script = backendScript();
    script.operations.push({ value: { items: [scriptedReceipt()] } });

    await open(script).schema.operations("page-2");
    expect(callsTo(script, "operations")[0].detail).toMatchObject({ cursor: "page-2" });
  });

  it("reports a cursor the ledger refuses as an invalid cursor rather than an empty page", async () => {
    const script = backendScript();
    script.operations.push({ fault: { kind: "invalid-cursor" } });

    await expect(open(script).schema.operations("another-owners-cursor"))
      .rejects.toMatchObject({ reason: "invalid-cursor", code: "invalid_cursor" });
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

  it("reads the operational system status with the probes that could not be sampled", async () => {
    const script = backendScript();
    const status = {
      version: "v1.2.3", databaseReachable: true, migrationCurrent: 4, migrationLatest: 5,
      migrationDirty: true, catalogVersion: 9, listenerReady: false,
      unavailable: [{ component: "listener", code: "unavailable" }],
      sampledAt: "2026-09-01T00:05:00Z", lastSuccessAt: "2026-09-01T00:00:00Z",
    };
    script.status.push({ value: status });

    await expect(open(script).system.status()).resolves.toEqual(status);
  });

  it("fails a system read rather than reporting a partial status", async () => {
    const script = backendScript();
    script.status.push({ fault: { kind: "unreachable" } });

    await expect(open(script).system.status()).rejects.toBeDefined();
  });

  // Each state has to survive the wire as itself: a drift report carrying its
  // capped findings, and the two states that both carry none.
  it.each([
    {
      name: "drift",
      health: {
        state: "drift" as const,
        findings: [{ severity: "error", code: "collection_index_missing", message: "A catalog field index is missing" }],
        findingCount: 40, unavailable: [], sampledAt: "2026-09-01T00:05:00Z",
        lastSuccessAt: "2026-09-01T00:05:00Z", retries: 0,
      },
    },
    {
      name: "healthy",
      health: {
        state: "healthy" as const, findings: [], findingCount: 0, unavailable: [],
        sampledAt: "2026-09-01T00:05:00Z", lastSuccessAt: "2026-09-01T00:05:00Z", retries: 0,
      },
    },
    {
      name: "unavailable",
      health: {
        state: "unavailable" as const, findings: [], findingCount: 0,
        unavailable: [{ component: "doctor", code: "timeout" }],
        sampledAt: "2026-09-01T00:05:00Z", lastSuccessAt: "2026-08-31T22:00:00Z", retries: 7,
      },
    },
  ])("reads a $name schema diagnosis", async ({ health }) => {
    const script = backendScript();
    script.health.push({ value: health });

    await expect(open(script).system.health()).resolves.toEqual(health);
  });

  it("fails a schema diagnosis read rather than reporting an empty one", async () => {
    const script = backendScript();
    script.health.push({ fault: { kind: "unreachable" } });

    await expect(open(script).system.health()).rejects.toBeDefined();
  });

  it("keeps a record failure distinct from a schema failure", async () => {
    const script = backendScript();
    script.read.push({ fault: { kind: "stale" } });

    await expect(open(script).records.get("posts", recordID)).rejects.toBeInstanceOf(RecordError);
  });
});
