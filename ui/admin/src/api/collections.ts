import { request } from "./http";
import type { CollectionScaffolds, OperationReceipt, OperationStatus } from "./schema";

// Contracts mirror internal/api/schema_handlers.go, schema_operations.go, rules.go,
// user_handlers.go (access) and internal/schema/types.go. Request bodies are decoded
// strictly (unknown keys are rejected), so input shapes are kept separate from the
// catalog and plan shapes the server returns.

const base = "/api/admin/collections";
const seg = encodeURIComponent;

// ---------------------------------------------------------------------------
// Field, index, view and collection vocabulary
// ---------------------------------------------------------------------------

export type FieldType =
  | "boolean"
  | "integer"
  | "bigint"
  | "numeric"
  | "text"
  | "editor"
  | "geoPoint"
  | "autodate"
  | "password"
  | "timestamptz"
  | "uuid"
  | "jsonb"
  | "relation"
  /** Metadata only: attachments live in the file store, the table has no column. */
  | "file";

export type CollectionType = "base" | "auth" | "view";
export type ValidationMode = "postgres" | "pocketbase";

export type RelationCardinality = "single" | "multiple";
export type RelationDeleteAction = "restrict" | "cascade" | "set_null";
export type RelationStorageKind = "inline_fk_v1" | "edge_table_v1";

/** One flat object on the wire; which keys a field type accepts is enforced by the
 * server (preview fails with validation_failed otherwise). Absent = unset. */
export interface FieldOptions {
  /** text, password: rune bounds. */
  minLength?: number;
  maxLength?: number;
  /** text, password: Go/RE2 pattern (max 1024 bytes). */
  pattern?: string;
  /** text only. */
  format?: "email" | "url";
  /** text with format email|url: host plus subdomains; deny wins. */
  allowedDomains?: string[];
  deniedDomains?: string[];
  /** text only: bounded RE2 generator used when the key is absent on create. Excludes a default. */
  autogeneratePattern?: string;
  /** text (enum) or jsonb (multi-select, up to 1000 distinct values). */
  allowedValues?: string[];
  /** jsonb with allowedValues: selection count bounds. file: maxSelect = attachment limit (default 1). */
  minSelect?: number;
  maxSelect?: number;
  /** integer, bigint, numeric: inclusive bounds as exact decimal strings. */
  min?: string;
  max?: string;
  /** file: per-attachment byte limit. */
  maxFileSize?: number;
  /** file: exact media types or wildcards such as image/*. */
  mimeTypes?: string[];
  /** file: thumbnail sizes such as "100x100", "0x300", "100x100t" (suffix t|b|f). */
  thumbs?: string[];
  /** editor: UTF-8 byte limit; 0/absent = 5 MiB, max 8 MiB. */
  maxSize?: number;
  /** editor: editing hint only, the server never rewrites URLs. */
  convertURLs?: boolean;
  /** autodate: at least one must be true. */
  onCreate?: boolean;
  onUpdate?: boolean;
  /** file: defaults to true; false opts the field into public delivery (/api/files/{id}). */
  protected?: boolean;
}

export type RecordIdPolicy =
  | { kind: "uuid" }
  /** Defaults when created via scaffolds: 15..15, ^[a-z0-9]+$, [a-z0-9]{15}. */
  | { kind: "text"; minLength?: number; maxLength?: number; pattern?: string; autogeneratePattern?: string };

/** One btree key: `field` and `expression` are mutually exclusive. */
export interface IndexKey {
  field?: string;
  expression?: string;
  collation?: string;
  order?: "asc" | "desc";
}

export interface IndexInput {
  name: string;
  fields?: string[];
  unique: boolean;
  keys?: IndexKey[];
  /** Partial-index predicate in PostgreSQL syntax (AST allowlisted by the planner). */
  where?: string;
  rawSql?: string;
}

/** Catalog index. */
export interface CollectionIndex {
  id: string;
  name: string;
  fields: string[] | null;
  /** Physical PostgreSQL index name. */
  indexName: string;
  unique: boolean;
  keys?: IndexKey[];
  where?: string;
  rawSql?: string;
}

/** Planned index (no identity yet). */
export interface PlannedIndex {
  name: string;
  fields: string[] | null;
  unique: boolean;
  keys?: IndexKey[];
  where?: string;
  rawSql?: string;
}

/** Declarative view filter tree. `value` is any JSON literal. */
export interface ViewFilter {
  op: string;
  args?: ViewFilter[];
  field?: string;
  value?: unknown;
}

/** Either a declarative view (source + projection [+ filter]) or a SQL view (`sql`). */
export interface ViewInput {
  source?: string;
  projection?: string[] | null;
  filter?: ViewFilter;
  sql?: string;
}

export interface ViewSpec {
  sourceCollectionId: string;
  source: string;
  projection: string[] | null;
  filter?: ViewFilter;
  sql?: string;
  dependencies?: string[];
}

export type PlannedView = ViewSpec;

export interface RelationInput {
  /** Collection name; may name the collection being created (self relation). */
  targetCollection: string;
  cardinality?: RelationCardinality;
  minSelect?: number;
  maxSelect?: number;
  onDelete?: RelationDeleteAction;
}

export interface RelationStorage {
  kind: RelationStorageKind;
  name: string;
}

export interface RelationSpec {
  targetCollectionId: string;
  cardinality: RelationCardinality;
  minSelect: number;
  maxSelect: number;
  onDelete: RelationDeleteAction;
  valueCodecVersion: number;
  storage: RelationStorage;
}

export interface PlannedRelation extends Omit<RelationSpec, "targetCollectionId"> {
  targetCollection: string;
  /** Absent only for a create-time self relation. */
  targetCollectionId?: string;
}

/** A field as sent in plan/apply and schema documents. Send `id` to keep a field's
 * identity across renames. */
export interface FieldInput {
  id?: string;
  name: string;
  type: FieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  /** JSON default literal for the field type. */
  default?: unknown;
  /** Required when type is "relation". */
  relation?: RelationInput;
  options?: FieldOptions;
  hidden?: boolean;
  presentable?: boolean;
  help?: string;
  system?: boolean;
}

/** A field as stored in the catalog. */
export interface CollectionField {
  id: string;
  name: string;
  columnName: string;
  type: FieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  default?: unknown;
  relation?: RelationSpec;
  options?: FieldOptions;
  ordinal: number;
  hidden: boolean;
  presentable: boolean;
  help?: string;
  system?: boolean;
}

/** A field in a plan definition: no column name, relation carries the symbolic target. */
export interface PlannedField {
  /** Absent for a field that does not exist yet. */
  id?: string;
  name: string;
  type: FieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  default?: unknown;
  relation?: PlannedRelation;
  options?: FieldOptions;
  ordinal: number;
  hidden: boolean;
  presentable: boolean;
  help?: string;
  system?: boolean;
}

export interface Collection {
  id: string;
  name: string;
  compatibilityName?: string;
  type?: CollectionType;
  system?: boolean;
  validationMode?: ValidationMode;
  /** Present only for text record ids; absent means uuid ids. */
  recordId?: RecordIdPolicy;
  tableName: string;
  fields: CollectionField[];
  indexes?: CollectionIndex[];
  view?: ViewSpec;
  createdAt: string;
}

export interface PlannedCollection {
  name: string;
  compatibilityName?: string;
  type: CollectionType;
  system?: boolean;
  validationMode: ValidationMode;
  recordId?: RecordIdPolicy;
  tableName: string;
  fields: PlannedField[];
  indexes?: PlannedIndex[];
  view?: PlannedView;
}

// ---------------------------------------------------------------------------
// Catalog
// ---------------------------------------------------------------------------

export interface CollectionCatalog {
  /** Schema version; pass as baseVersion only via a plan, never directly. */
  version: number;
  /** Keyed by collection name. */
  collections: Record<string, Collection>;
}

export function listCollections(signal?: AbortSignal): Promise<CollectionCatalog> {
  return request("GET", base, { signal });
}

/** `collection` may be the name, compatibility name or id. */
export function getCollection(collection: string, signal?: AbortSignal): Promise<Collection> {
  return request("GET", `${base}/${seg(collection)}`, { signal });
}

/** Portable starting points; replace the empty names, then import via schema preview/apply. */
export function getCollectionScaffolds(signal?: AbortSignal): Promise<CollectionScaffolds> {
  return request("GET", `${base}/scaffolds`, { signal });
}

// ---------------------------------------------------------------------------
// Plan -> apply
// ---------------------------------------------------------------------------

interface CollectionAttributes {
  compatibilityName?: string;
  type?: CollectionType;
  system?: boolean;
  validationMode?: ValidationMode;
  recordId?: RecordIdPolicy;
}

/** `view` (declarative or { sql }) and `viewQuery` (SQL shorthand) are mutually exclusive. */
type ViewSource =
  | { view?: ViewInput; viewQuery?: never }
  | { viewQuery: string; view?: never };

export type CreateCollectionChange = CollectionAttributes &
  ViewSource & {
    operation: "create";
    name: string;
    fields?: FieldInput[];
    indexes?: IndexInput[];
  };

/** `fields` is the complete desired field list (omitted fields are dropped). It is
 * required unless `collectionId` is sent. `collectionId` selects the collection so
 * `name` can rename it. */
export type UpdateCollectionChange = CollectionAttributes & {
  operation: "update";
  name: string;
  collectionId?: string;
} & (
    | { fields?: FieldInput[]; indexes?: IndexInput[]; view?: never; viewQuery?: never }
    /** View update: fields and indexes are inferred and must be omitted. */
    | ({ fields?: never; indexes?: never } & ({ view: ViewInput; viewQuery?: never } | { viewQuery: string; view?: never }))
  );

export interface DeleteCollectionChange {
  operation: "delete";
  name: string;
}

export type CollectionChange = CreateCollectionChange | UpdateCollectionChange | DeleteCollectionChange;

export interface CollectionPlan {
  baseVersion: number;
  checksum: string;
  definition: PlannedCollection;
  /** DDL statements the apply will run. */
  sql: string[];
}

export function planCollectionChange(change: CollectionChange, signal?: AbortSignal): Promise<CollectionPlan> {
  return request("POST", `${base}/plan`, { body: change, signal });
}

/** Client-generated operation identity. Keep both to recover the outcome via
 * getSchemaOperation(operationId) if the apply response is lost. */
export interface ApplyIdentity {
  /** Non-nil UUID. */
  operationId: string;
  /** 16..128 bytes; reuse it only to retry the same request. */
  idempotencyKey: string;
}

export interface CollectionDeleted {
  name: string;
  deleted: true;
}

/** Returned instead of a collection when the apply was replayed (200), its outcome had
 * to be read from the ledger (200 committed), or is not decided yet (202: poll statusUrl). */
export interface SchemaOperationOutcome {
  operationId: string;
  status: OperationStatus;
  /** /api/admin/schema-operations/{operationId} */
  statusUrl: string;
  receipt?: OperationReceipt;
}

/** create/update -> Collection; delete -> CollectionDeleted; replay/unknown -> outcome.
 * A terminal ledger failure is thrown as ApiError 409 schema_operation_failed with
 * details { operation: [failureCode] }. */
export type CollectionApplyResult = Collection | CollectionDeleted | SchemaOperationOutcome;

/** Send the same change that was planned, plus the plan's checksum and baseVersion. */
export function applyCollectionChange(
  change: CollectionChange,
  plan: Pick<CollectionPlan, "checksum" | "baseVersion">,
  identity: ApplyIdentity,
  signal?: AbortSignal,
): Promise<CollectionApplyResult> {
  return request("POST", `${base}/apply`, {
    body: { ...change, checksum: plan.checksum, baseVersion: plan.baseVersion },
    headers: { "PgBase-Operation-ID": identity.operationId, "Idempotency-Key": identity.idempotencyKey },
    signal,
  });
}

// ---------------------------------------------------------------------------
// SQL view sample
// ---------------------------------------------------------------------------

/** Raw planner field: most keys are PascalCase on the wire (untagged Go struct). */
export interface ViewSampleField {
  id?: string;
  Name: string;
  ColumnName: string;
  Type: FieldType;
  Required: boolean;
  Unique: boolean;
  Indexed: boolean;
  Default: unknown;
  relation?: PlannedRelation;
  options?: FieldOptions;
  hidden?: boolean;
  presentable?: boolean;
  help?: string;
  system?: boolean;
  Ordinal: number;
}

export interface ViewSample {
  fields: ViewSampleField[];
  /** Up to 10 rows; null when the query returns none. */
  items: Record<string, unknown>[] | null;
  /** Collection ids the query reads. */
  dependencies: string[];
}

/** `query` is a SELECT of at most 5000 bytes. */
export function sampleView(query: string, signal?: AbortSignal): Promise<ViewSample> {
  return request("POST", `${base}/view/sample`, { body: { query }, signal });
}

// ---------------------------------------------------------------------------
// Typed access rules
// ---------------------------------------------------------------------------

export type RuleOp =
  | "allow"
  | "deny"
  | "owner"
  | "authenticated"
  | "unauthenticated"
  | "all"
  | "any"
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "contains"
  | "not_contains"
  | "any_eq"
  | "any_ne"
  | "any_lt"
  | "any_lte"
  | "any_gt"
  | "any_gte"
  | "any_contains"
  | "any_not_contains";

export interface RuleFunctionArg {
  field?: string;
  value?: unknown;
  macro?: string;
}

/** Bounded rule AST (depth <= 8, <= 64 nodes). allow/deny/owner/(un)authenticated take
 * no operands; all/any take 1..16 `args`; comparisons take `field` plus exactly one of
 * `value`, `auth` or `otherField`. `pocketbase`, `function` and `macro` come from
 * PocketBase-expression conversion. */
export interface AccessRule {
  op: RuleOp;
  args?: AccessRule[];
  field?: string;
  otherField?: string;
  value?: unknown;
  auth?: "id" | "verified" | "collectionId";
  pocketbase?: string;
  function?: { name: string; args: RuleFunctionArg[] };
  macro?: string;
}

export interface CollectionRules {
  list: AccessRule;
  view: AccessRule;
  create: AccessRule;
  update: AccessRule;
  delete: AccessRule;
}

export interface RulePolicy {
  collection: string;
  rules: CollectionRules;
  revision: number;
}

export function getCollectionRules(collection: string, signal?: AbortSignal): Promise<RulePolicy> {
  return request("GET", `${base}/${seg(collection)}/rules`, { signal });
}

export function setCollectionRules(collection: string, rules: CollectionRules): Promise<RulePolicy> {
  return request("PUT", `${base}/${seg(collection)}/rules`, { body: { rules } });
}

/** PocketBase filter strings: null = superusers only (deny), "" = everyone. */
export interface PocketBaseRules {
  listRule: string | null;
  viewRule: string | null;
  createRule: string | null;
  updateRule: string | null;
  deleteRule: string | null;
}

export interface PocketBaseRulePolicy {
  collection: string;
  pocketbaseRules: PocketBaseRules;
  revision: number;
}

export function getPocketBaseRules(collection: string, signal?: AbortSignal): Promise<PocketBaseRulePolicy> {
  return request("GET", `${base}/${seg(collection)}/rules`, { query: { compat: "pocketbase" }, signal });
}

export function setPocketBaseRules(collection: string, pocketbaseRules: PocketBaseRules): Promise<PocketBaseRulePolicy> {
  return request("PUT", `${base}/${seg(collection)}/rules`, {
    query: { compat: "pocketbase" },
    body: { pocketbaseRules },
  });
}

/** Simulated request for a rule preview. Keys are PascalCase on the wire (untagged Go struct). */
export interface RulePreviewRequest {
  Present?: Record<string, boolean>;
  Changed?: Record<string, boolean>;
  Method?: string;
  Body?: Record<string, unknown>;
  Query?: Record<string, string[]>;
  Headers?: Record<string, string[]>;
  Auth?: Record<string, unknown>;
  Context?: Record<string, unknown>;
}

export interface RulePreviewInput {
  rule: AccessRule;
  action: "list" | "view" | "update" | "delete";
  /** Internal user UUID to evaluate as; omitted = anonymous. */
  userId?: string;
  /** 1..50 distinct internal record UUIDs. */
  recordIds: string[];
  request?: RulePreviewRequest;
}

export interface RuleDecision {
  recordId: string;
  allowed: boolean;
}

export async function previewCollectionRule(collection: string, input: RulePreviewInput): Promise<RuleDecision[]> {
  return (await request<{ items: RuleDecision[] }>("POST", `${base}/${seg(collection)}/rules/preview`, { body: input })).items;
}

// ---------------------------------------------------------------------------
// User access gate and truncate
// ---------------------------------------------------------------------------

export interface CollectionUserAccess {
  collection: string;
  enabled: boolean;
}

export function getCollectionUserAccess(collection: string, signal?: AbortSignal): Promise<CollectionUserAccess> {
  return request("GET", `${base}/${seg(collection)}/user-access`, { signal });
}

export function setCollectionUserAccess(collection: string, enabled: boolean): Promise<CollectionUserAccess> {
  return request("PUT", `${base}/${seg(collection)}/user-access`, { body: { enabled } });
}

export interface TruncateResult {
  name: string;
  truncated: boolean;
  recordsDeleted: number;
  filesDeleted: number;
  relationsDeleted: number;
}

/** Deletes every record. The server requires `collection` to be the exact collection name. */
export function truncateCollection(collection: string): Promise<TruncateResult> {
  return request("POST", `${base}/${seg(collection)}/truncate`, { body: { name: collection } });
}
