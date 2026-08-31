import { APIClientError, type APIClient } from "./client";
import type { LosslessNumber } from "lossless-json";
import { stringifyJSON } from "./json";

export type RecordValue = boolean | number | string | null | LosslessNumber | Record<string, unknown> | unknown[];
export type RecordData = Record<string, RecordValue>;
export interface RecordPage { items: RecordData[]; nextCursor?: string }
export interface RecordRepresentation { data: RecordData; etag: string }
export interface CreatedRecord extends RecordRepresentation { replayed: boolean }

export class CreateAttempt {
  private body: string | null = null;
  private key: string | null = null;

  constructor(private readonly generateKey: () => string = () => crypto.randomUUID()) {}

  keyFor(input: RecordData): string {
    const body = stringifyJSON(input);
    if (this.body !== body || this.key === null) {
      this.body = body;
      this.key = this.generateKey();
    }
    return this.key;
  }

  reset(): void {
    this.body = null;
    this.key = null;
  }
}

function recordsPath(collection: string): string {
  return `/api/v1/collections/${encodeURIComponent(collection)}/records`;
}

export interface ListOptions {
  cursor?: string;
  filter?: string;
  sort?: string;
}

export class RecordsAPI {
  constructor(private readonly client: APIClient, private readonly csrfToken: string) {}

  list(collection: string, options?: ListOptions, signal?: AbortSignal): Promise<RecordPage> {
    const query = new URLSearchParams({ limit: "50" });
    if (options?.cursor) query.set("cursor", options.cursor);
    if (options?.filter) query.set("filter", options.filter);
    if (options?.sort) query.set("sort", options.sort);
    return this.client.request<RecordPage>(`${recordsPath(collection)}?${query}`, { signal });
  }

  async get(collection: string, id: string, signal?: AbortSignal): Promise<RecordRepresentation> {
    const response = await this.client.requestWithResponse<RecordData>(`${recordsPath(collection)}/${encodeURIComponent(id)}`, { signal });
    return { data: response.data, etag: requiredETag(response.headers) };
  }

  async create(collection: string, input: RecordData, idempotencyKey: string): Promise<CreatedRecord> {
    const response = await this.client.requestWithResponse<RecordData>(recordsPath(collection), {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey, "X-PgBase-CSRF": this.csrfToken },
      body: stringifyJSON(input),
    });
    return {
      data: response.data,
      etag: requiredETag(response.headers),
      replayed: response.headers.get("Idempotency-Replayed") === "true",
    };
  }

  async update(collection: string, id: string, input: RecordData, etag: string): Promise<RecordRepresentation> {
    const response = await this.client.requestWithResponse<RecordData>(`${recordsPath(collection)}/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: { "If-Match": etag, "X-PgBase-CSRF": this.csrfToken },
      body: stringifyJSON(input),
    });
    return { data: response.data, etag: requiredETag(response.headers) };
  }

  async delete(collection: string, id: string, etag: string): Promise<void> {
    await this.client.request<void>(`${recordsPath(collection)}/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers: { "If-Match": etag, "X-PgBase-CSRF": this.csrfToken },
    });
  }
}

function requiredETag(headers: Headers): string {
  const etag = headers.get("ETag")?.trim();
  if (etag === undefined || !isStrongETag(etag)) {
    throw new APIClientError(502, "invalid_response", {}, "Record response did not include a valid strong ETag", headers);
  }
  return etag;
}

function isStrongETag(value: string): boolean {
  if (value.length < 2 || value[0] !== '"' || value[value.length - 1] !== '"') return false;
  for (let index = 1; index < value.length - 1; index += 1) {
    const code = value.charCodeAt(index);
    if (code !== 0x21 && !(code >= 0x23 && code <= 0x7e) && !(code >= 0x80 && code <= 0xff)) return false;
  }
  return true;
}
