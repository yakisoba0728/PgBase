import { request } from "./http";
import type {
  ApplyIdentity,
  CollectionPlan,
  CollectionRules,
  CollectionType,
  FieldInput,
  IndexInput,
  RecordIdPolicy,
  ValidationMode,
  ViewInput,
} from "./collections";

// Contracts mirror internal/api/schema_document.go, schema_operations.go and
// internal/schema/{schema_document,auth_schema_document,operation_types,scaffolds}.go.

const seg = encodeURIComponent;

// ---------------------------------------------------------------------------
// Portable schema document (pgbase.schema/v1)
// ---------------------------------------------------------------------------

export interface SchemaDocumentCollection {
  name: string;
  compatibilityName?: string;
  type?: CollectionType;
  system?: boolean;
  validationMode?: ValidationMode;
  recordId?: RecordIdPolicy;
  fields?: FieldInput[];
  indexes?: IndexInput[];
  view?: ViewInput;
  dependsOn?: string[];
  userAccess: boolean;
  rules: CollectionRules | null;
}

/** PocketBase-style filter strings; null = no access, "" = everyone. */
export interface AuthCollectionRules {
  list: string | null;
  view: string | null;
  create: string | null;
  update: string | null;
  delete: string | null;
  auth: string | null;
  manage: string | null;
}

export interface AuthCollectionPolicy {
  passwordEnabled: boolean;
  minimumPasswordBytes: number;
  maximumPasswordBytes: number;
  passwordPattern: string;
  identityFields: string[];
  mfaEnabled: boolean;
  mfaDurationSeconds: number;
  mfaRule: string;
  otpEnabled: boolean;
  otpDurationSeconds: number;
  otpMaxAttempts: number;
}

export interface SchemaDocumentAuthCollection {
  name: string;
  registrationEnabled: boolean;
  fields?: FieldInput[];
  recordId?: RecordIdPolicy;
  rules: AuthCollectionRules;
  policy: AuthCollectionPolicy;
  /** Token purpose -> TTL seconds. */
  tokenTTLs?: Record<string, number>;
}

export interface SchemaDocument {
  format: "pgbase.schema/v1";
  collections: SchemaDocumentCollection[];
  authCollections?: SchemaDocumentAuthCollection[];
  /** Plan deletion of catalog collections absent from `collections`. */
  deleteMissing?: boolean;
}

export interface CollectionScaffolds {
  base: SchemaDocumentCollection;
  /** Give both names the same value and import them in one preview/apply. */
  auth: { collection: SchemaDocumentCollection; authCollection: SchemaDocumentAuthCollection };
  view: SchemaDocumentCollection;
}

export function exportSchema(signal?: AbortSignal): Promise<SchemaDocument> {
  return request("GET", "/api/admin/schema/export", { signal });
}

export interface SchemaImportPreview {
  baseVersion: number;
  checksum: string;
  policyChecksum: string;
  /** Ordered per-collection plans; an auth registration step repeats its collection with `sql: null`. */
  changes: (Omit<CollectionPlan, "sql"> & { sql: string[] | null })[];
  /** Normalized document; apply must send the document that was previewed. */
  document: SchemaDocument;
}

export function previewSchemaImport(document: SchemaDocument, signal?: AbortSignal): Promise<SchemaImportPreview> {
  return request("POST", "/api/admin/schema/import/preview", { body: document, signal });
}

export interface SchemaImportResult {
  operationId: string;
  status: "running" | "committed" | "unknown" | "failed";
  failureCode?: string;
  version?: number;
  /** Number of collection changes applied. */
  applied?: number;
}

/** Requires the exact previewed document, baseVersion and checksum, and an operation identity. */
export function applySchemaImport(
  input: { document: SchemaDocument; baseVersion: number; checksum: string },
  identity: ApplyIdentity,
): Promise<SchemaImportResult> {
  return request("POST", "/api/admin/schema/import/apply", {
    body: input,
    headers: { "PgBase-Operation-ID": identity.operationId, "Idempotency-Key": identity.idempotencyKey },
  });
}

export function getSchemaImportOperation(operationId: string, signal?: AbortSignal): Promise<SchemaImportResult> {
  return request("GET", `/api/admin/schema/import/operations/${seg(operationId)}`, { signal });
}

// ---------------------------------------------------------------------------
// Collection apply ledger (owner-scoped)
// ---------------------------------------------------------------------------

export type OperationStatus = "running" | "unknown" | "committed" | "failed";
export type SchemaOperationKind = "collection_create" | "collection_update" | "collection_delete";
export type OperationFailureCode = "plan_conflict" | "apply_failed" | "lease_lost" | "reconcile_failed";

export interface OperationReceipt {
  operationId: string;
  operation: SchemaOperationKind;
  collectionId: string;
  status: OperationStatus;
  baseVersion: number;
  committedVersion?: number;
  failureCode?: OperationFailureCode;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
}

export interface OperationPage {
  /** Newest first. */
  items: OperationReceipt[];
  nextCursor?: string;
}

/** limit 0..100 (0/absent = 20). */
export function listSchemaOperations(
  options: { limit?: number; cursor?: string; signal?: AbortSignal } = {},
): Promise<OperationPage> {
  return request("GET", "/api/admin/schema-operations", {
    query: { limit: options.limit, cursor: options.cursor },
    signal: options.signal,
  });
}

export function getSchemaOperation(operationId: string, signal?: AbortSignal): Promise<OperationReceipt> {
  return request("GET", `/api/admin/schema-operations/${seg(operationId)}`, { signal });
}
