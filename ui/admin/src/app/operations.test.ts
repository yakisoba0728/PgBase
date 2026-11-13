import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import { findOperation, forgetOperations, operations, operationState, pollWhileRunning, trackOperation, untrackOperation, type TrackedOperation } from "./operations";

const now = 1_000_000_000;
const op = (kind: TrackedOperation["kind"], extra: Partial<TrackedOperation> = {}): TrackedOperation => ({ kind, id: "x", startedAt: now, ...extra });

describe("running operations", () => {
  it("reads progress and outcome per kind without inventing percentages", () => {
    expect(operationState(op("backup"), { status: "running", phase: "copying_objects_3_of_12" })).toEqual({ state: "running", progress: "25%" });
    expect(operationState(op("backup"), { status: "running", phase: "dumping_database" })).toEqual({ state: "running", progress: undefined });
    expect(operationState(op("restore"), { status: "completed" }).state).toBe("done");
    expect(operationState(op("migration"), { id: "x", status: "copying", copiedObjects: 4, totalObjects: 10 })).toEqual({ state: "running", progress: "4/10" });
    expect(operationState(op("migration"), { id: "x", status: "canceled" }).state).toBe("failed");
    expect(operationState(op("migration"), null).state).toBe("gone");
    expect(operationState(op("schema"), { status: "unknown" }).state).toBe("attention");
    expect(operationState(op("import"), { status: "committed" }).state).toBe("done");
    // Not answered yet (or a transient error): still running, never dropped.
    expect(operationState(op("schema"), undefined).state).toBe("running");
    // A requested job run: queued until it starts, then its own outcome.
    expect(operationState(op("job", { since: "2026-01-01T00:00:00Z" }), { running: false, lastStartedAt: "2026-01-01T00:00:00Z" }, false, now + 1000).state).toBe("running");
    expect(operationState(op("job"), { running: false }, false, now + 3 * 60_000).state).toBe("gone");
    expect(operationState(op("job"), { running: true }).state).toBe("running");
    expect(operationState(op("job"), { running: false, failed: false, lastStartedAt: "2026-01-02T00:00:00Z" }).state).toBe("done");
    expect(operationState(op("job"), { running: false, failed: true, lastStartedAt: "2026-01-02T00:00:00Z" }).state).toBe("failed");
  });

  it("does not let a lost start or an older migration end an entry early", () => {
    // The latest migration is an older, finished one: not this start's outcome.
    expect(operationState(op("migration"), { id: "older", status: "committed" }, false, now + 1000).state).toBe("running");
    expect(operationState(op("migration"), { id: "older", status: "committed" }, false, now + 10 * 60 * 1000).state).toBe("gone");
    // A 404 right after a lost apply keeps the replay identity; without one the entry goes.
    expect(operationState(op("schema", { idempotencyKey: "k" }), undefined, true, now + 5000).state).toBe("attention");
    expect(operationState(op("schema", { idempotencyKey: "k" }), undefined, true, now + 2 * 60 * 60 * 1000).state).toBe("gone");
    expect(operationState(op("backup"), undefined, true, now).state).toBe("gone");
  });

  it("persists entries and lets exactly one caller report an outcome", () => {
    trackOperation({ kind: "schema", id: "op-1", subject: "posts", checksum: "c", idempotencyKey: "k" });
    expect(findOperation("schema", "posts")?.idempotencyKey).toBe("k");
    expect(localStorage.getItem("pgbase.operations.op-1")).toContain("op-1");
    expect(untrackOperation("op-1")).toBe(true);
    expect(untrackOperation("op-1")).toBe(false);
    expect(findOperation("schema")).toBeUndefined();
  });
});

describe("pollWhileRunning", () => {
  const query = (data: unknown, error: unknown = null) => ({ state: { data, error } }) as unknown as Parameters<typeof pollWhileRunning>[0];
  it("polls running applies and keeps asking until a first status arrives", () => {
    expect(pollWhileRunning(query({ status: "running" }))).toBe(2000);
    expect(pollWhileRunning(query({ status: "committed" }))).toBe(false);
    expect(pollWhileRunning(query(undefined, new ApiError(503, "schema_unavailable", "")))).toBe(5000);
    expect(pollWhileRunning(query(undefined, new ApiError(404, "not_found", "")))).toBe(false);
    expect(pollWhileRunning(query(undefined, new ApiError(401, "unauthorized", "")))).toBe(false);
  });
});

it("retains another tab's operation when this tab adds and removes its own", () => {
  localStorage.setItem("pgbase.operations.foreign", JSON.stringify({ kind: "schema", id: "foreign", startedAt: Date.now(), idempotencyKey: "recover" }));
  trackOperation({ kind: "schema", id: "mine" });
  untrackOperation("mine");
  expect(findOperation("schema")?.id).toBe("foreign");
  expect(localStorage.getItem("pgbase.operations.foreign")).toContain("recover");
  untrackOperation("foreign");
});

it("retains independent operation identities in memory when storage writes are refused", () => {
  forgetOperations();
  const write = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("quota"); });
  trackOperation({ kind: "schema", id: "first", idempotencyKey: "a" });
  trackOperation({ kind: "schema", id: "second", idempotencyKey: "b" });
  expect(operations().map((operation) => operation.id)).toEqual(["first", "second"]);
  untrackOperation("first");
  expect(operations().map((operation) => operation.id)).toEqual(["second"]);
  forgetOperations(); write.mockRestore();
});
