import type { APIClient } from "./client";
import { stringifyJSON } from "./json";

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

export type CollectionOperation = "create" | "update" | "delete";

export interface CollectionChangeInput {
  operation: CollectionOperation;
  name: string;
  fields?: FieldInput[];
}

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

export interface CollectionDeleteResult { name: string; deleted: boolean }

export class CollectionsAPI {
  constructor(private readonly client: APIClient, private readonly csrfToken: string) {}

  catalog(signal?: AbortSignal): Promise<CollectionCatalog> {
    return this.client.request<CollectionCatalog>("/api/admin/collections", { signal });
  }

  plan(input: CollectionChangeInput): Promise<CollectionPlan> {
    return this.client.request<CollectionPlan>("/api/admin/collections/plan", {
      method: "POST",
      body: stringifyJSON(input),
    });
  }

  apply(input: CollectionChangeInput, checksum: string, baseVersion: number): Promise<CollectionDefinition | CollectionDeleteResult> {
    return this.client.request<CollectionDefinition | CollectionDeleteResult>("/api/admin/collections/apply", {
      method: "POST",
      headers: { "X-PgBase-CSRF": this.csrfToken },
      body: stringifyJSON({ ...input, checksum, baseVersion }),
    });
  }
}
