import { request, requestBlob, requestWithHeaders } from "./http";

// Contracts mirror internal/api/record_handlers.go, batch_handlers.go and file_handlers.go.
// An administrator cookie session uses /api/v1/collections/... for records (the
// /api/v1/user, /service and /public prefixes belong to other principals) and
// /api/admin/... for files.
//
// ETags travel in response headers for single-record calls (returned alongside the
// record); batch results carry `etag` in the body. Without If-Match an
// update/delete is last-write-wins; without Idempotency-Key a create is not retry-safe.

const seg = encodeURIComponent;
const recordsPath = (collection: string) => `/api/v1/collections/${seg(collection)}/records`;

/** Field values are arbitrary JSON (unsafe numbers arrive as exact non-number values). */
export interface RecordData {
  /** Public id: uuid, or text for collections with a text record-id policy. */
  id: string;
  /** RFC 3339 UTC; may be absent when `fields` projects them away. */
  created_at?: string;
  updated_at?: string;
  /** Present when `expand` was requested: relation field -> related record(s). */
  expand?: Record<string, RecordData | RecordData[] | null>;
  [field: string]: unknown;
}

export interface RecordPage {
  items: RecordData[];
  /** Present when more records follow; pass back as `cursor`. */
  nextCursor?: string;
}

export interface ListRecordsOptions {
  /** e.g. `title ~ 'draft' && published = true`. */
  filter?: string;
  /** Comma-separated, `-` prefix for descending; system fields are id, created_at, updated_at. */
  sort?: string;
  /** Comma-separated relation paths. */
  expand?: string;
  /** Comma-separated projection. */
  fields?: string;
  cursor?: string;
  /** 1..200, default 50. */
  limit?: number;
  signal?: AbortSignal;
}

export function listRecords(collection: string, options: ListRecordsOptions = {}): Promise<RecordPage> {
  const { signal, ...query } = options;
  return request("GET", recordsPath(collection), { query: { ...query }, signal });
}

/** A record plus its strong ETag (pass back as `ifMatch`). */
export interface RecordWithETag {
  record: RecordData;
  etag: string;
}

async function withETag(response: Promise<{ data: RecordData; headers: Headers }>): Promise<RecordWithETag> {
  const { data, headers } = await response;
  return { record: data, etag: headers.get("ETag") ?? "" };
}

export function getRecord(
  collection: string,
  id: string,
  options: { expand?: string; fields?: string; signal?: AbortSignal } = {},
): Promise<RecordWithETag> {
  return withETag(
    requestWithHeaders("GET", `${recordsPath(collection)}/${seg(id)}`, {
      query: { expand: options.expand, fields: options.fields },
      signal: options.signal,
    }),
  );
}

/** `input` maps field names to JSON values. `idempotencyKey`: 16..128 chars of
 * [A-Za-z0-9._~-]; reuse it to retry the same create safely. */
export function createRecord(
  collection: string,
  input: Record<string, unknown>,
  options: { idempotencyKey?: string; fields?: string } = {},
): Promise<RecordWithETag> {
  return withETag(
    requestWithHeaders("POST", recordsPath(collection), {
      body: input,
      query: { fields: options.fields },
      headers: options.idempotencyKey ? { "Idempotency-Key": options.idempotencyKey } : undefined,
    }),
  );
}

/** Partial update. `ifMatch` is an ETag (or "*"); a mismatch throws ApiError 412 precondition_failed. */
export function updateRecord(
  collection: string,
  id: string,
  input: Record<string, unknown>,
  options: { ifMatch?: string; fields?: string } = {},
): Promise<RecordWithETag> {
  return withETag(
    requestWithHeaders("PATCH", `${recordsPath(collection)}/${seg(id)}`, {
      body: input,
      query: { fields: options.fields },
      headers: options.ifMatch ? { "If-Match": options.ifMatch } : undefined,
    }),
  );
}

export async function deleteRecord(collection: string, id: string, options: { ifMatch?: string } = {}): Promise<void> {
  await request("DELETE", `${recordsPath(collection)}/${seg(id)}`, {
    headers: options.ifMatch ? { "If-Match": options.ifMatch } : undefined,
  });
}

// ---------------------------------------------------------------------------
// Batch (atomic)
// ---------------------------------------------------------------------------

export type BatchOperation =
  | { operation: "create"; collection: string; input: Record<string, unknown>; idempotencyKey?: string }
  | { operation: "update" | "upsert"; collection: string; id: string; input: Record<string, unknown>; ifMatch?: string }
  | { operation: "delete"; collection: string; id: string; ifMatch?: string };

export interface BatchResult {
  eventId: string;
  /** Absent for deletes. */
  record?: RecordData;
  etag?: string;
  version?: number;
}

/** All operations commit or none do. The per-request limit is a runtime setting;
 * a disabled batch throws ApiError 503 batch_disabled. */
export async function batchRecords(operations: BatchOperation[], options: { fields?: string } = {}): Promise<BatchResult[]> {
  return (
    await request<{ results: BatchResult[] }>("POST", "/api/v1/collections/batch", {
      body: { operations },
      query: { fields: options.fields },
    })
  ).results;
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

/** "original" (default), "thumbnail", or a size from the field's `thumbs` option. */
export type FileVariant = string;

export interface StoredFile {
  id: string;
  field: string;
  filename: string;
  contentType: string;
  size: number;
  thumbnail: boolean;
  protected: boolean;
  /** Present for unprotected files: /api/files/{id}. */
  publicUrl?: string;
}

const recordFilesPath = (collection: string, id: string, field: string) =>
  `/api/admin/collections/${seg(collection)}/records/${seg(id)}/files/${seg(field)}`;

export async function listRecordFiles(
  collection: string,
  id: string,
  field: string,
  signal?: AbortSignal,
): Promise<StoredFile[]> {
  return (await request<{ items: StoredFile[] }>("GET", recordFilesPath(collection, id, field), { signal })).items;
}

/** Uploads one attachment; the server verifies the declared size and SHA-256. */
export async function uploadRecordFile(
  collection: string,
  id: string,
  field: string,
  file: File,
  signal?: AbortSignal,
): Promise<StoredFile> {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", await file.arrayBuffer()));
  const form = new FormData();
  form.append("file", file, file.name);
  return request("POST", recordFilesPath(collection, id, field), {
    raw: form,
    headers: {
      "X-PgBase-File-Size": String(file.size),
      "X-PgBase-File-SHA256": Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join(""),
    },
    signal,
  });
}

/** Short-lived token required by downloadFile, bound to one variant. */
export async function mintFileToken(fileId: string, variant: FileVariant = "original"): Promise<string> {
  return (await request<{ token: string }>("POST", `/api/admin/files/${seg(fileId)}/token`, { body: { variant } })).token;
}

export function downloadFile(fileId: string, token: string, variant?: FileVariant, signal?: AbortSignal): Promise<Blob> {
  return requestBlob(`/api/admin/files/${seg(fileId)}`, { query: { token, variant }, signal });
}

export async function deleteFile(fileId: string): Promise<void> {
  await request("DELETE", `/api/admin/files/${seg(fileId)}`);
}

/** Unauthenticated URL; works only for files whose field has `protected: false`. */
export function publicFileURL(fileId: string, variant?: FileVariant): string {
  const path = `/api/files/${seg(fileId)}`;
  return variant ? `${path}?variant=${seg(variant)}` : path;
}
