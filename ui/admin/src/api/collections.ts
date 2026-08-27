import type { APIClient } from "./client";

export const fieldTypes = ["boolean", "integer", "bigint", "numeric", "text", "timestamptz", "uuid", "jsonb"] as const;
export type FieldType = typeof fieldTypes[number];

export interface FieldInput {
  name: string;
  type: FieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  default?: unknown;
}

export interface CreateCollectionInput { name: string; fields: FieldInput[] }

export interface FieldDefinition extends FieldInput {
  id?: string;
  columnName?: string;
  ordinal: number;
}

export interface CollectionDefinition {
  id?: string;
  name: string;
  tableName: string;
  fields: FieldDefinition[];
  createdAt?: string;
}

export interface CollectionPlan {
  baseVersion: number;
  checksum: string;
  definition: CollectionDefinition;
  sql: string[];
}

export interface CollectionCatalog {
  version: number;
  collections: Record<string, CollectionDefinition>;
}

export class CollectionsAPI {
  constructor(private readonly client: APIClient, private readonly csrfToken: string) {}

  catalog(signal?: AbortSignal): Promise<CollectionCatalog> {
    return this.client.request<CollectionCatalog>("/api/admin/collections", { signal });
  }

  plan(input: CreateCollectionInput): Promise<CollectionPlan> {
    return this.client.request<CollectionPlan>("/api/admin/collections/plan", {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  apply(input: CreateCollectionInput, checksum: string, baseVersion: number): Promise<CollectionDefinition> {
    return this.client.request<CollectionDefinition>("/api/admin/collections/apply", {
      method: "POST",
      headers: { "X-PgBase-CSRF": this.csrfToken },
      body: JSON.stringify({ ...input, checksum, baseVersion }),
    });
  }
}
