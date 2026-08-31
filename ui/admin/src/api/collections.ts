import type { APIClient } from "./client";
import { stringifyJSON } from "./json";

export const fieldTypes = ["boolean", "integer", "bigint", "numeric", "text", "timestamptz", "uuid", "jsonb", "relation"] as const;
export type FieldType = typeof fieldTypes[number];

export const relationCardinalities = ["single", "multiple"] as const;
export type RelationCardinality = typeof relationCardinalities[number];

export const relationDeleteActions = ["restrict", "cascade", "set_null"] as const;
export type RelationDeleteAction = typeof relationDeleteActions[number];

export interface RelationInput {
  targetCollection: string;
  cardinality: RelationCardinality;
  minSelect: number;
  maxSelect: number;
  onDelete: RelationDeleteAction;
}

export interface RelationStorage {
  kind: "inline_fk_v1" | "edge_table_v1";
  name: string;
}

export interface PlannedRelationDefinition extends RelationInput {
  targetCollectionId?: string;
  valueCodecVersion: number;
  storage: RelationStorage;
}

export interface RelationDefinition {
  targetCollectionId: string;
  cardinality: RelationCardinality;
  minSelect: number;
  maxSelect: number;
  onDelete: RelationDeleteAction;
  valueCodecVersion: number;
  storage: RelationStorage;
}

export interface FieldInput {
  name: string;
  type: FieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  default?: unknown;
  relation?: RelationInput;
}

export type CollectionOperation = "create" | "update" | "delete";

export interface CollectionChangeInput {
  operation: CollectionOperation;
  name: string;
  fields?: FieldInput[];
}

export interface PlannedFieldDefinition extends Omit<FieldInput, "relation"> {
  relation?: PlannedRelationDefinition;
  ordinal: number;
}

export interface FieldDefinition extends Omit<FieldInput, "relation"> {
  id?: string;
  columnName?: string;
  relation?: RelationDefinition;
  ordinal: number;
}

export interface PlannedCollectionDefinition {
  name: string;
  tableName: string;
  fields: PlannedFieldDefinition[];
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
  definition: PlannedCollectionDefinition;
  sql: string[];
}

export interface CollectionCatalog {
  version: number;
  collections: Record<string, CollectionDefinition>;
}

export function collectionFromCatalog(catalog: CollectionCatalog, name: string): CollectionDefinition | undefined {
  return Object.hasOwn(catalog.collections, name) ? catalog.collections[name] : undefined;
}

export function collectionFromCatalogByID(catalog: CollectionCatalog, id: string): CollectionDefinition | undefined {
  for (const name of Object.keys(catalog.collections)) {
    const collection = collectionFromCatalog(catalog, name);
    if (collection?.id === id) return collection;
  }
  return undefined;
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
      headers: { "X-PgBase-CSRF": this.csrfToken },
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
