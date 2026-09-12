import {
  CollectionCatalog,
  PlannedChange,
  SchemaError,
  type AdminBackend,
  type ApplyAttempt,
  type ApplyOutcome,
  type CollectionChange,
  type CollectionDefinition,
  type FieldDefinition,
  type OperationHistory,
  type OperationReceipt,
  type OperationStatus,
  type PlannedField,
  type RecordData,
  type RecordRules,
  type RulePreview,
  type SchemaDocument,
  type SchemaImportPlan,
  type SchemaImportResult,
  type CollectionScaffolds,
} from "./ports";
import { WireError, type Reply, type Request } from "./http-transport";

const operationsPath = "/api/admin/schema-operations";
const historyPageSize = 20;
const maximumHistoryPageSize = 100;

export interface SchemaTransport {
  send(request: Request): Promise<Reply>;
  schemaCall(request: Request): Promise<Reply>;
  applyFailure(caught: unknown, plan: PlannedChange): Promise<unknown>;
}

function definitions(raw: unknown): CollectionDefinition[] {
  return Object.values(
    (raw as { collections?: Record<string, CollectionDefinition> }).collections ?? {},
  );
}

function receipt(raw: unknown): OperationReceipt {
  const wire = raw as Record<string, unknown>;
  const bounded: OperationReceipt = {
    operationId: String(wire.operationId ?? ""), operation: String(wire.operation ?? ""),
    collectionId: String(wire.collectionId ?? ""), status: wire.status as OperationStatus,
    baseVersion: Number(wire.baseVersion ?? 0), createdAt: String(wire.createdAt ?? ""),
    updatedAt: String(wire.updatedAt ?? ""),
  };
  if (wire.committedVersion !== undefined) bounded.committedVersion = Number(wire.committedVersion);
  if (wire.failureCode !== undefined) bounded.failureCode = String(wire.failureCode);
  if (wire.completedAt !== undefined) bounded.completedAt = String(wire.completedAt);
  return bounded;
}

function applyOutcome(data: unknown, operationId: string): ApplyOutcome {
  const body = data as { status?: unknown; fields?: unknown } | undefined;
  if (typeof body?.status !== "string")
    return Array.isArray(body?.fields) ? { kind: "applied", definition: data as CollectionDefinition } : { kind: "applied" };
  return body.status === "committed" ? { kind: "applied" } : { kind: "undecided", operationId };
}

function isUncertainImportApplyError(caught: unknown): boolean {
  if (caught instanceof SchemaError) return caught.code === "invalid_response_body";
  if (caught instanceof WireError) return caught.wire.code === "invalid_response_body";
  return true;
}

export function createSchemaPort(transport: SchemaTransport): AdminBackend["schema"] {
  return {
    catalog: async (signal) => {
      const reply = await transport.schemaCall({ path: "/api/admin/collections", signal });
      return new CollectionCatalog((reply.data as { version: number }).version, definitions(reply.data));
    },
    plan: async (change: CollectionChange, signal) => {
      const reply = await transport.schemaCall({ path: "/api/admin/collections/plan", method: "POST", body: change, csrf: true, signal });
      const plan = reply.data as { baseVersion: number; checksum: string; definition: { fields: PlannedField[] }; sql: string[] };
      return new PlannedChange(change, plan.checksum, plan.baseVersion, plan.definition.fields, Array.isArray(plan.sql) ? plan.sql : []);
    },
    apply: async (plan: PlannedChange, attempt: ApplyAttempt) => {
      const identity = attempt.identityFor(plan);
      try {
        return applyOutcome((await transport.send({ path: "/api/admin/collections/apply", method: "POST", body: plan.applyRequest(), csrf: true, headers: { "PgBase-Operation-ID": identity.operationId, "Idempotency-Key": identity.idempotencyKey } })).data, identity.operationId);
      } catch (caught) {
        if (!(caught instanceof WireError) || caught.wire.code === "invalid_response_body")
          return { kind: "undecided", operationId: identity.operationId };
        throw await transport.applyFailure(caught, plan);
      }
    },
    operation: async (operationId, signal) => receipt((await transport.schemaCall({ path: `${operationsPath}/${encodeURIComponent(operationId)}`, signal })).data),
    operations: async (cursor, signal): Promise<OperationHistory> => {
      const query = new URLSearchParams({ limit: String(historyPageSize) });
      if (cursor !== undefined && cursor !== "") query.set("cursor", cursor);
      const page = (await transport.schemaCall({ path: `${operationsPath}?${query}`, signal })).data as OperationHistory;
      if (page.items.length > maximumHistoryPageSize) throw new SchemaError("failed", "invalid_response_body");
      return { items: page.items.map(receipt), ...(page.nextCursor === undefined ? {} : { nextCursor: page.nextCursor }) };
    },
    userAccess: async (collection, signal) => (await transport.schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/user-access`, signal })).data as { collection: string; enabled: boolean },
    setUserAccess: async (collection, enabled) => (await transport.schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/user-access`, method: "PUT", body: { enabled }, csrf: true })).data as { collection: string; enabled: boolean },
    exportDocument: async (signal) => (await transport.schemaCall({ path: "/api/admin/schema/export", signal })).data as SchemaDocument,
    previewImport: async (document: SchemaDocument, signal) => ({ ...((await transport.schemaCall({ path: "/api/admin/schema/import/preview", method: "POST", body: document, csrf: true, signal })).data as SchemaImportPlan), operationId: crypto.randomUUID(), idempotencyKey: crypto.randomUUID() }),
    applyImport: async (plan: SchemaImportPlan, signal) => {
      try {
        return (await transport.schemaCall({ path: "/api/admin/schema/import/apply", method: "POST", body: { document: plan.document, baseVersion: plan.baseVersion, checksum: plan.checksum }, headers: { "PgBase-Operation-ID": plan.operationId ?? "", "Idempotency-Key": plan.idempotencyKey ?? "" }, csrf: true, signal })).data as SchemaImportResult;
      } catch (caught) {
        if (plan.operationId && isUncertainImportApplyError(caught)) {
          try {
            return (await transport.schemaCall({ path: `/api/admin/schema/import/operations/${encodeURIComponent(plan.operationId)}`, signal })).data as SchemaImportResult;
          } catch {
            throw caught;
          }
        }
        throw caught;
      }
    },
    sampleView: async (query, signal) => (await transport.schemaCall({ path: "/api/admin/collections/view/sample", method: "POST", body: { query }, csrf: true, signal })).data as { fields: FieldDefinition[]; items: RecordData[]; dependencies: string[] },
    scaffolds: async (signal) => (await transport.schemaCall({ path: "/api/admin/collections/scaffolds", signal })).data as CollectionScaffolds,
    truncate: async (collection, confirmation, signal) => (await transport.schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/truncate`, method: "POST", body: { name: confirmation }, csrf: true, signal })).data as { name: string; truncated: true; recordsDeleted: number; filesDeleted: number; relationsDeleted: number },
    importStatus: async (operationId, signal) => (await transport.schemaCall({ path: `/api/admin/schema/import/operations/${encodeURIComponent(operationId)}`, signal })).data as SchemaImportResult,
  };
}
