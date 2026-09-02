import type { LosslessNumber } from "lossless-json";
import { stringifyJSON } from "./json";

interface Administrator { id: string; email: string; createdAt: string }
export interface AuthenticatedSession { administrator: Administrator; expiresAt: string }
export interface SetupInput { setupToken: string; email: string; password: string }
export interface LoginInput { email: string; password: string }

export type AuthSnapshot =
  | { kind: "loading" }
  | { kind: "setup-required" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; administrator: Administrator; expiresAt: string };

// `setup-unknown` and `unreachable` both end anonymous but tell the
// administrator different things, so the port keeps them apart.
export type ResumedSession =
  | { kind: "authenticated"; session: AuthenticatedSession }
  | { kind: "setup-required" }
  | { kind: "anonymous" }
  | { kind: "setup-unknown" }
  | { kind: "unreachable" };

export type SessionFailure = "invalid-credentials" | "setup-unavailable" | "invalid-input" | "failed";

export class SessionError extends Error {
  constructor(readonly reason: SessionFailure) { super(reason); this.name = "SessionError"; }
}

export const fieldTypes = ["boolean", "integer", "bigint", "numeric", "text", "timestamptz", "uuid", "jsonb", "relation"] as const;
export const relationCardinalities = ["single", "multiple"] as const;
export const relationDeleteActions = ["restrict", "cascade", "set_null"] as const;
export type FieldType = typeof fieldTypes[number];
export type RelationCardinality = typeof relationCardinalities[number];
export type RelationDeleteAction = typeof relationDeleteActions[number];

interface RelationInput { targetCollection: string; cardinality: RelationCardinality; minSelect: number; maxSelect: number; onDelete: RelationDeleteAction }
interface RelationDefinition extends Omit<RelationInput, "targetCollection"> { targetCollectionId: string }

export interface FieldInput { name: string; type: FieldType; required: boolean; unique: boolean; indexed: boolean; default?: unknown; relation?: RelationInput }

export interface FieldDefinition extends Omit<FieldInput, "relation"> { relation?: RelationDefinition; ordinal: number }
export interface PlannedField extends Omit<FieldInput, "relation"> { relation?: RelationInput }
export interface CollectionDefinition { id?: string; name: string; tableName: string; fields: FieldDefinition[] }
export interface CollectionChange { operation: "create" | "update" | "delete"; name: string; fields?: FieldInput[] }

// A Map keyed by own entries, so a collection named `constructor` or
// `__proto__` can neither be found by accident nor shadow a real one.
export class CollectionCatalog {
  readonly #byName: Map<string, CollectionDefinition>;

  constructor(readonly version: number, collections: readonly CollectionDefinition[]) {
    this.#byName = new Map(collections.map((collection) => [collection.name, collection]));
  }

  get(name: string): CollectionDefinition | undefined { return this.#byName.get(name); }

  byID(id: string): CollectionDefinition | undefined {
    for (const collection of this.#byName.values()) if (collection.id === id) return collection;
    return undefined;
  }

  all(): CollectionDefinition[] { return [...this.#byName.values()].sort((left, right) => left.name.localeCompare(right.name)); }
}

// The checksum never leaves the plan it was issued for, so no caller can apply
// one collection's approved plan under another collection's name or version.
export class PlannedChange {
  readonly #change: CollectionChange;
  readonly #checksum: string;
  readonly #signature: string;

  constructor(
    change: CollectionChange,
    checksum: string,
    readonly baseVersion: number,
    readonly fields: readonly PlannedField[],
    readonly sql: readonly string[],
  ) {
    this.#change = change;
    this.#checksum = checksum;
    this.#signature = stringifyJSON(change);
  }

  get operation(): CollectionChange["operation"] { return this.#change.operation; }
  get collection(): string { return this.#change.name; }
  matches(change: CollectionChange): boolean { return stringifyJSON(change) === this.#signature; }
  applyRequest(): Record<string, unknown> { return { ...this.#change, checksum: this.#checksum, baseVersion: this.baseVersion }; }
}

export const operationStatuses = ["running", "unknown", "committed", "failed"] as const;
export type OperationStatus = typeof operationStatuses[number];

/**
 * The redacted, owner-scoped view of one durable schema operation. Every field
 * the ledger publishes is bounded and non-identifying; raw SQL, the request
 * hash and the idempotency key have no representation here at all, so nothing
 * a receipt can carry is a secret.
 */
export interface OperationReceipt {
  operationId: string;
  operation: string;
  collectionId: string;
  status: OperationStatus;
  baseVersion: number;
  committedVersion?: number;
  failureCode?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
}

export interface OperationHistory { items: OperationReceipt[]; nextCursor?: string }

/**
 * An apply either committed or is not decidable yet. The identity travels with
 * the undecided answer because that is the only case where the outcome still
 * has to be asked for; a caller that gets one must not read it as either a
 * success or a failure.
 */
export type ApplyOutcome = { kind: "applied" } | { kind: "undecided"; operationId: string };

export interface OperationIdentity { operationId: string; idempotencyKey: string }

/**
 * One operation identity per approved plan, minted before the request it can no
 * longer observe. Retrying the same plan replays the first attempt server-side;
 * a re-previewed plan gets a new identity, because the same identity bound to a
 * different request is a conflict rather than a replay.
 */
export class ApplyAttempt {
  #request: string | null = null;
  #identity: OperationIdentity | null = null;

  constructor(private readonly generateID: () => string = () => crypto.randomUUID()) {}

  identityFor(plan: PlannedChange): OperationIdentity {
    const request = stringifyJSON(plan.applyRequest());
    if (this.#request !== request || this.#identity === null) {
      this.#request = request;
      this.#identity = { operationId: this.generateID(), idempotencyKey: this.generateID() };
    }
    return this.#identity;
  }
}

type SchemaFailure =
  | "same-version-conflict" | "catalog-moved" | "data-blocked" | "referenced"
  | "operation-conflict" | "operation-failed" | "invalid-cursor" | "not-found" | "failed";

export class SchemaError extends Error {
  constructor(readonly reason: SchemaFailure, readonly code: string, readonly details: Record<string, string[]> = {}) { super(reason); this.name = "SchemaError"; }
}

export type RecordValue = boolean | number | string | null | LosslessNumber | Record<string, unknown> | unknown[];
export type RecordData = Record<string, RecordValue>;
export interface RecordPage { items: RecordData[]; nextCursor?: string }
export interface ListOptions { cursor?: string; filter?: string; sort?: string }

// The precondition travels with the record it was read from, so a caller
// cannot send one record's ETag with another record's identity.
export class VersionedRecord {
  readonly #etag: string;

  constructor(readonly collection: string, readonly id: string, readonly data: RecordData, etag: string) {
    this.#etag = etag;
  }

  precondition(): string { return this.#etag; }
}

// One idempotency key per unchanged create body: an identical retry replays the
// first attempt server-side, and any edit starts a new attempt.
export class CreateAttempt {
  #body: string | null = null;
  #key: string | null = null;

  constructor(readonly collection: string, private readonly generateKey: () => string = () => crypto.randomUUID()) {}

  keyFor(input: RecordData): string {
    const body = stringifyJSON(input);
    if (this.#body !== body || this.#key === null) {
      this.#body = body;
      this.#key = this.generateKey();
    }
    return this.#key;
  }

  reset(): void { this.#body = null; this.#key = null; }
}

type RecordFailure = "not-found" | "stale" | "rejected" | "failed";

export class RecordError extends Error {
  constructor(readonly reason: RecordFailure, readonly details: Record<string, string[]> = {}) { super(reason); this.name = "RecordError"; }
}

export interface SystemStatus {
  version: string; databaseReachable: boolean; migrationCurrent: number; migrationLatest: number;
  migrationDirty: boolean; catalogVersion: number; listenerReady: boolean;
}

export interface SessionPort {
  resume(signal?: AbortSignal): Promise<ResumedSession>;
  setup(input: SetupInput): Promise<AuthenticatedSession>;
  login(input: LoginInput): Promise<AuthenticatedSession>;
  logout(): Promise<void>;
  onExpired(listener: () => void): () => void;
}

export interface SchemaPort {
  catalog(signal?: AbortSignal): Promise<CollectionCatalog>;
  plan(change: CollectionChange, signal?: AbortSignal): Promise<PlannedChange>;
  apply(plan: PlannedChange, attempt: ApplyAttempt): Promise<ApplyOutcome>;
  operation(operationId: string, signal?: AbortSignal): Promise<OperationReceipt>;
  // History is read one bounded page at a time. There is deliberately no limit
  // to pass: a caller that cannot ask for a larger page cannot be handed one.
  operations(cursor?: string, signal?: AbortSignal): Promise<OperationHistory>;
}

export interface RecordPort {
  list(collection: string, options: ListOptions, signal?: AbortSignal): Promise<RecordPage>;
  get(collection: string, id: string, signal?: AbortSignal): Promise<VersionedRecord>;
  create(attempt: CreateAttempt, input: RecordData): Promise<VersionedRecord>;
  update(record: VersionedRecord, patch: RecordData): Promise<VersionedRecord>;
  remove(record: VersionedRecord): Promise<void>;
}

export interface SystemPort {
  status(signal?: AbortSignal): Promise<SystemStatus>;
}

export interface AdminBackend {
  session: SessionPort;
  schema: SchemaPort;
  records: RecordPort;
  system: SystemPort;
}
