import {
  applyCollectionChange,
  planCollectionChange,
  type ApplyIdentity,
  type Collection,
  type CollectionApplyResult,
  type CollectionChange,
  type CollectionPlan,
} from "../api/collections";
import { ApiError } from "../api/http";
import { applySchemaImport, getSchemaImportOperation, getSchemaOperation, type SchemaImportPreview } from "../api/schema";

// Apply with the plan's identifiers, then settle the outcome: a 202 or a replay
// returns a ledger status that may still be running, so it is polled briefly.

/** The server has not decided yet; retrying with the same identity is safe. */
export class Undecided extends Error {
  constructor(readonly operationId: string) {
    super("operation outcome not decided");
  }
}

const bytes = (n: number) => crypto.getRandomValues(new Uint8Array(n));
const hex = (values: Uint8Array) => Array.from(values, (byte) => byte.toString(16).padStart(2, "0")).join("");

// getRandomValues also works on plain-HTTP hosts, where crypto.randomUUID is absent.
export function newIdentity(): ApplyIdentity {
  const b = bytes(16);
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = hex(b);
  return { operationId: `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`, idempotencyKey: hex(bytes(16)) };
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function settle(operationId: string, initial: string, read: () => Promise<{ status: string; failureCode?: string }>) {
  let status = initial;
  for (let attempt = 0; status === "running" || status === "unknown"; attempt++) {
    if (attempt >= 15) throw new Undecided(operationId);
    await sleep(1000);
    const receipt = await read();
    status = receipt.status;
    if (status === "failed") throw new ApiError(409, "schema_operation_failed", receipt.failureCode ?? "failed");
  }
  if (status === "failed") throw new ApiError(409, "schema_operation_failed", "failed");
}

/** Resolves to the applied collection, or undefined when only a receipt came back. */
export async function applyCollection(change: CollectionChange, plan: CollectionPlan, identity: ApplyIdentity): Promise<Collection | undefined> {
  const result: CollectionApplyResult = await applyCollectionChange(change, plan, identity);
  if ("statusUrl" in result) {
    await settle(result.operationId, result.status, async () => {
      const receipt = await getSchemaOperation(result.operationId);
      return { status: receipt.status, failureCode: receipt.failureCode };
    });
    return undefined;
  }
  return "fields" in result ? result : undefined;
}

export async function applyImport(preview: SchemaImportPreview, identity: ApplyIdentity): Promise<void> {
  const result = await applySchemaImport({ document: preview.document, baseVersion: preview.baseVersion, checksum: preview.checksum }, identity);
  await settle(result.operationId, result.status, () => getSchemaImportOperation(result.operationId));
}

/** A delete refused because the auth collection still has users: the schema delete reports
 * `name: auth_collection_has_users`, the registration delete `id: has_users`. */
export const hasUsers = (failure: unknown) =>
  failure instanceof ApiError && JSON.stringify(failure.details ?? {}).includes("has_users");

/** Plans and applies a delete in one step (the confirm dialog is the review). The server removes an auth
 * collection's sign-in registration in the same transaction, and refuses while it still has users. */
export async function deleteCollection(collection: Collection): Promise<void> {
  const change: CollectionChange = { operation: "delete", name: collection.name };
  const plan = await planCollectionChange(change);
  await applyCollection(change, plan, newIdentity());
}
