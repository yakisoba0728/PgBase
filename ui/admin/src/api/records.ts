import type { APIClient } from "./client";

export type RecordValue = boolean | number | string | null | Record<string, unknown> | unknown[];
export type RecordData = Record<string, RecordValue>;
export interface RecordPage { items: RecordData[]; nextCursor?: string }

function recordsPath(collection: string): string {
  return `/api/v1/collections/${encodeURIComponent(collection)}/records`;
}

export class RecordsAPI {
  constructor(private readonly client: APIClient, private readonly csrfToken: string) {}

  list(collection: string, cursor?: string, signal?: AbortSignal): Promise<RecordPage> {
    const query = new URLSearchParams({ limit: "50" });
    if (cursor) query.set("cursor", cursor);
    return this.client.request<RecordPage>(`${recordsPath(collection)}?${query}`, { signal });
  }

  get(collection: string, id: string, signal?: AbortSignal): Promise<RecordData> {
    return this.client.request<RecordData>(`${recordsPath(collection)}/${encodeURIComponent(id)}`, { signal });
  }

  create(collection: string, input: RecordData): Promise<RecordData> {
    return this.mutate(recordsPath(collection), "POST", input);
  }

  update(collection: string, id: string, input: RecordData): Promise<RecordData> {
    return this.mutate(`${recordsPath(collection)}/${encodeURIComponent(id)}`, "PATCH", input);
  }

  async delete(collection: string, id: string): Promise<void> {
    await this.client.request<void>(`${recordsPath(collection)}/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers: { "X-PgBase-CSRF": this.csrfToken },
    });
  }

  private mutate(path: string, method: "POST" | "PATCH", input: RecordData): Promise<RecordData> {
    return this.client.request<RecordData>(path, {
      method,
      headers: { "X-PgBase-CSRF": this.csrfToken },
      body: JSON.stringify(input),
    });
  }
}
