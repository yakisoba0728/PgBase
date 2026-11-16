import { afterEach, expect, it, vi } from "vitest";
import * as collections from "../api/collections";
import * as schema from "../api/schema";
import { advanceSession } from "../api/http";
import { applyCollection, applyImport, deleteCollection, newIdentity } from "./apply";

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

it("does not apply a delete planned in an earlier administrator session", async () => {
  let finish!: (plan: collections.CollectionPlan) => void;
  vi.spyOn(collections, "planCollectionChange").mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  const apply = vi.spyOn(collections, "applyCollectionChange").mockResolvedValue({ deleted: true, name: "notes" });
  const pending = deleteCollection({ name: "notes" } as collections.Collection);
  advanceSession();
  finish({ baseVersion: 1, checksum: "checksum" } as collections.CollectionPlan);
  await expect(pending).rejects.toMatchObject({ code: "session_changed" });
  expect(apply).not.toHaveBeenCalled();
});

it.each(["collection", "import"])("stops %s outcome polling before it can use a replacement session", async (kind) => {
  vi.useFakeTimers();
  vi.spyOn(collections, "applyCollectionChange").mockResolvedValue({ operationId: "operation", status: "running", statusUrl: "/operation" });
  vi.spyOn(schema, "applySchemaImport").mockResolvedValue({ operationId: "operation", status: "running" });
  const read = vi.spyOn(schema, "getSchemaOperation").mockResolvedValue({ status: "committed" } as schema.OperationReceipt);
  const readImport = vi.spyOn(schema, "getSchemaImportOperation").mockResolvedValue({ operationId: "operation", status: "committed" });
  const pending = kind === "collection"
    ? applyCollection({ operation: "delete", name: "notes" }, {} as collections.CollectionPlan, newIdentity())
    : applyImport({ document: {} } as schema.SchemaImportPreview, newIdentity());
  // Let the initial apply settle and enter the explicit one-second polling wait.
  await vi.advanceTimersByTimeAsync(0);
  expect(vi.getTimerCount()).toBeGreaterThan(0);
  advanceSession();
  const rejected = expect(pending).rejects.toMatchObject({ code: "session_changed" });
  await vi.advanceTimersByTimeAsync(1000);
  await rejected;
  expect(read).not.toHaveBeenCalled();
  expect(readImport).not.toHaveBeenCalled();
});
