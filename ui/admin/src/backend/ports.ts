import type { LosslessNumber } from "lossless-json";
import type { StoragePort } from "./storage-port";
import type { BackupStoragePort } from "./backup-storage-port";
import { stringifyJSON } from "./json";

interface Administrator { id: string; email: string; createdAt: string }
export interface AuthenticatedSession { administrator: Administrator; expiresAt: string; sqlConsoleEnabled?: boolean }
export interface SetupInput { setupToken: string; email: string; password: string }
export interface SetupBackup { id: string; createdAt?: string; size?: number; postgresqlMajor?: number }
export interface SetupBackupJob { id: string; status?: string; phase?: string; error?: string }
export interface LoginInput { email: string; password: string; otpCode?: string }

export type AuthSnapshot =
  | { kind: "loading" }
  | { kind: "setup-required" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; administrator: Administrator; expiresAt: string; sqlConsoleEnabled?: boolean };

// `setup-unknown` and `unreachable` both end anonymous but tell the
// administrator different things, so the port keeps them apart.
export type ResumedSession =
  | { kind: "authenticated"; session: AuthenticatedSession }
  | { kind: "setup-required" }
  | { kind: "anonymous" }
  | { kind: "setup-unknown" }
  | { kind: "unreachable" };

export type SessionFailure = "invalid-credentials" | "otp-required" | "setup-unavailable" | "invalid-input" | "failed";

export class SessionError extends Error {
  constructor(readonly reason: SessionFailure) { super(reason); this.name = "SessionError"; }
}

export const fieldTypes = ["boolean", "integer", "bigint", "numeric", "text", "timestamptz", "uuid", "jsonb", "file", "relation", "editor", "geoPoint", "autodate", "password"] as const;
export const relationCardinalities = ["single", "multiple"] as const;
export const relationDeleteActions = ["restrict", "cascade", "set_null"] as const;
export type FieldType = typeof fieldTypes[number];
export type RelationCardinality = typeof relationCardinalities[number];
export type RelationDeleteAction = typeof relationDeleteActions[number];

interface RelationInput { targetCollection: string; cardinality: RelationCardinality; minSelect: number; maxSelect: number; onDelete: RelationDeleteAction }
interface RelationDefinition extends Omit<RelationInput, "targetCollection"> { targetCollectionId: string }

export interface FieldOptions { maxSize?: number; convertURLs?: boolean; onCreate?: boolean; onUpdate?: boolean; minLength?: number; maxLength?: number; pattern?: string; autogeneratePattern?: string; format?: "email" | "url"; allowedDomains?: string[]; deniedDomains?: string[]; allowedValues?: string[]; min?: string; max?: string; minSelect?: number; maxSelect?: number; maxFileSize?: number; mimeTypes?: string[]; thumbs?: string[] }
export interface FieldInput { id?: string; options?: FieldOptions; name: string; type: FieldType; required: boolean; unique: boolean; indexed: boolean; hidden?: boolean; presentable?: boolean; help?: string; default?: unknown; relation?: RelationInput }

export interface FieldDefinition extends Omit<FieldInput, "relation"> { relation?: RelationDefinition; ordinal: number }
export interface PlannedField extends Omit<FieldInput, "relation"> { relation?: RelationInput }
export interface CompoundIndex { id?: string; name: string; indexName?: string; fields: string[]; unique: boolean }
export interface ViewFilter { op: "all" | "any" | "eq" | "ne" | "lt" | "lte" | "gt" | "gte"; args?: ViewFilter[]; field?: string; value?: unknown }
export type SQLViewDefinition = { sql: string; dependencies?: string[]; source?: never; projection?: never; filter?: never; sourceCollectionId?: never };
export type ViewDefinition =
	| { sourceCollectionId?: string; source: string; projection: string[]; filter?: ViewFilter; sql?: never; dependencies?: never }
	| SQLViewDefinition;
export interface CollectionDefinition { id?: string; name: string; tableName: string; fields: FieldDefinition[]; indexes?: CompoundIndex[]; view?: ViewDefinition }
export type CollectionChange =
	| { operation: "create"; name: string; fields?: FieldInput[]; indexes?: CompoundIndex[]; view?: ViewDefinition }
	| { operation: "update"; name: string; fields: FieldInput[]; indexes?: CompoundIndex[]; view?: never }
	| { operation: "update"; name: string; view: SQLViewDefinition; fields?: never; indexes?: never }
	| { operation: "delete"; name: string; fields?: never; indexes?: never; view?: never };

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
 * success or a failure. `definition` is the collection the change produced, and
 * is absent whenever the answer was a ledger receipt rather than a collection
 * body: a replay stores the receipt, not the collection the first attempt
 * returned, so there is nothing to reconstruct it from.
 */
export type ApplyOutcome =
  | { kind: "applied"; definition?: CollectionDefinition }
  | { kind: "undecided"; operationId: string };

export interface OperationIdentity { operationId: string; idempotencyKey: string }

/**
 * One operation identity per approved plan, minted before the request it can no
 * longer observe. Retrying the same plan replays the first attempt server-side;
 * a re-previewed plan gets a new identity, because the same identity bound to a
 * different request is a conflict rather than a replay.
 */
export class ApplyAttempt {
  #plan: PlannedChange | null = null;
  #identity: OperationIdentity | null = null;

  constructor(private readonly generateID: () => string = () => crypto.randomUUID()) {}

  identityFor(plan: PlannedChange): OperationIdentity {
    // A new preview is a new approval; only retrying its same object reuses keys.
    if (this.#plan !== plan || this.#identity === null) {
      this.#plan = plan;
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

// unavailable names every probe whose value this sample could not read, which
// is the only thing separating a genuinely healthy zero from one nobody could
// obtain: the numeric members keep their last confirmed reading either way.
export interface SystemStatus {
  version: string; databaseReachable: boolean; migrationCurrent: number; migrationLatest: number;
  migrationDirty: boolean; catalogVersion: number; listenerReady: boolean;
  unavailable: SystemStatusProbe[]; sampledAt: string; lastSuccessAt: string;
}

export interface SystemStatusProbe { component: string; code: string }

/**
 * The redacted result of the most recent background diagnostic run. state is
 * read first because the three outcomes are not derivable from the finding
 * list: a run that never completed and a run that found nothing both carry
 * none. findingCount is the uncapped total, so a truncated list is visible as
 * one rather than read as the whole report.
 */
export interface SchemaHealth {
  state: "healthy" | "drift" | "unavailable";
  findings: SchemaFinding[]; findingCount: number;
  unavailable: SystemStatusProbe[]; sampledAt: string; lastSuccessAt: string; retries: number;
}

export interface SchemaFinding { severity: string; code: string; message: string }

export interface SessionPort {
  resume(signal?: AbortSignal): Promise<ResumedSession>;
  setup(input: SetupInput): Promise<AuthenticatedSession>;
  login(input: LoginInput): Promise<AuthenticatedSession>;
  logout(): Promise<void>;
	uploadSetupBackup?(setupToken: string, archive: File, signal?: AbortSignal): Promise<SetupBackup>;
	restoreSetupBackup?(setupToken: string, id: string, signal?: AbortSignal): Promise<SetupBackupJob>;
	setupBackupJob?(setupToken: string, id: string, signal?: AbortSignal): Promise<SetupBackupJob>;
	requestPasswordReset?(email: string, signal?: AbortSignal): Promise<void>;
	confirmPasswordReset?(token: string, newPassword: string, signal?: AbortSignal): Promise<void>;
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
	userAccess(collection: string, signal?: AbortSignal): Promise<{ collection: string; enabled: boolean }>;
	setUserAccess(collection: string, enabled: boolean): Promise<{ collection: string; enabled: boolean }>;
	exportDocument?(signal?: AbortSignal): Promise<SchemaDocument>;
	previewImport?(document: SchemaDocument, signal?: AbortSignal): Promise<SchemaImportPlan>;
	applyImport?(plan: SchemaImportPlan, signal?: AbortSignal): Promise<SchemaImportResult>;
	importStatus?(operationId: string, signal?: AbortSignal): Promise<SchemaImportResult>;
	/** Validates a prospective SQL view without storing it and returns its bounded sample. */
	sampleView?(query: string, signal?: AbortSignal): Promise<{ fields: FieldDefinition[]; items: RecordData[]; dependencies: string[] }>;
}
export interface SchemaDocumentCollection { name: string; fields?: FieldInput[]; indexes?: CompoundIndex[]; view?: ViewDefinition; dependsOn?: string[]; userAccess: boolean; rules: Record<string, unknown> }
export interface SchemaDocument { format: "pgbase.schema/v1"; collections: SchemaDocumentCollection[] }
export interface SchemaImportPreviewChange { operation: string; baseVersion: number; definition: { name: string; tableName: string; fields: PlannedField[] }; sql: string[] }
export interface SchemaImportPlan { baseVersion: number; checksum: string; policyChecksum: string; changes: SchemaImportPreviewChange[]; document: SchemaDocument; operationId?: string; idempotencyKey?: string }
export interface SchemaImportResult { operationId: string; status: "running" | "unknown" | "committed" | "failed"; version: number; applied: number }
export interface RecordRules { collection: string; rules: Record<string, unknown>; revision: number }
export interface RulePreview { items: Array<{ recordId: string; allowed: boolean }> }
export interface RecordRulesPort {
  getRules(collection: string, signal?: AbortSignal): Promise<RecordRules>;
  setRules(collection: string, rules: Record<string, unknown>, revision: number, signal?: AbortSignal): Promise<RecordRules>;
  previewRule(collection: string, value: { rule: Record<string, unknown>; action: "list" | "view" | "update" | "delete"; userId?: string; recordIds: string[] }, signal?: AbortSignal): Promise<RulePreview>;
}
export interface AuthCollection { id: string; name: string; registrationEnabled: boolean; profileSchema: Record<string, string> }
export interface AuthPolicy { passwordEnabled: boolean; minimumPasswordBytes: number; mfaEnabled: boolean; otpEnabled: boolean; otpDurationSeconds: number; otpMaxAttempts: number }
export interface OAuthProviderSettings { name: string; enabled: boolean; authorizationUrl: string; tokenUrl: string; userInfoUrl: string; clientId: string; clientSecret?: string; secretConfigured: boolean; scopes: string[]; redirectUris: string[]; subjectField: string; emailField: string; emailVerifiedField: string; avatarField: string; allowInsecureLoopback: boolean }
export interface AuthMailTemplate { purpose: "verify" | "password_reset" | "email_change" | "otp"; subject: string; body: string; htmlBody: string }
export interface AuthMailPreview { subject: string; text: string; html: string }
export interface AuthSMTPSettings { address: string; from: string; username: string; password?: string; passwordConfigured: boolean; serverName: string; implicitTls: boolean; allowInsecureLoopback: boolean }
export interface AuthAdministration { policy: AuthPolicy; providers: OAuthProviderSettings[]; templates: AuthMailTemplate[]; smtp?: AuthSMTPSettings }
export interface AuthCollectionsPort {
  listAuthCollections(signal?: AbortSignal): Promise<AuthCollection[]>; createAuthCollection(value: AuthCollection, signal?: AbortSignal): Promise<AuthCollection>; updateAuthCollection(id: string, value: Pick<AuthCollection, "registrationEnabled" | "profileSchema">, signal?: AbortSignal): Promise<AuthCollection>; deleteAuthCollection(id: string, signal?: AbortSignal): Promise<void>;
  getAdministration(id: string, signal?: AbortSignal): Promise<AuthAdministration>; setPolicy(id: string, value: AuthPolicy, signal?: AbortSignal): Promise<AuthPolicy>; putProvider(id: string, value: OAuthProviderSettings, signal?: AbortSignal): Promise<OAuthProviderSettings>; deleteProvider(id: string, name: string, signal?: AbortSignal): Promise<void>; putTemplate(id: string, value: AuthMailTemplate, signal?: AbortSignal): Promise<AuthMailTemplate>; previewTemplate(id: string, value: AuthMailTemplate & { actionUrl: string }, signal?: AbortSignal): Promise<AuthMailPreview>; putSMTP(id: string, value: AuthSMTPSettings, signal?: AbortSignal): Promise<AuthSMTPSettings>; testSMTP(id: string, recipient: string, signal?: AbortSignal): Promise<void>;
}
export interface ServiceTokenScope { collectionId: string; action: "read" | "create" | "update" | "delete" }
export interface ServiceToken { id: string; name: string; active: boolean; expiresAt: string; revokedAt?: string; createdAt: string; scopes: ServiceTokenScope[] }
export interface IssuedServiceToken extends ServiceToken { token: string }
export interface ServiceTokensPort {
  listServiceTokens(signal?: AbortSignal): Promise<ServiceToken[]>;
  issueServiceToken(value: { name: string; ttlSeconds: number; scopes: ServiceTokenScope[] }, signal?: AbortSignal): Promise<IssuedServiceToken>;
  revokeServiceToken(id: string, signal?: AbortSignal): Promise<void>;
}

export interface RecordPort {
  list(collection: string, options: ListOptions, signal?: AbortSignal): Promise<RecordPage>;
  get(collection: string, id: string, signal?: AbortSignal): Promise<VersionedRecord>;
  create(attempt: CreateAttempt, input: RecordData): Promise<VersionedRecord>;
	createWithFiles(attempt: CreateAttempt, input: RecordData, files: Record<string, File[]>): Promise<VersionedRecord>;
  update(record: VersionedRecord, patch: RecordData): Promise<VersionedRecord>;
	updateWithFiles(record: VersionedRecord, patch: RecordData, files: Record<string, File[]>): Promise<VersionedRecord>;
  remove(record: VersionedRecord): Promise<void>;
}
export interface FileInfo { id: string; field: string; filename: string; contentType: string; size: number; thumbnail: boolean; protected?: boolean; publicUrl?: string }
export interface FilePort {
  listFiles(collection: string, recordID: string, field: string, signal?: AbortSignal): Promise<FileInfo[]>;
  uploadFile(collection: string, recordID: string, field: string, file: File, signal?: AbortSignal): Promise<FileInfo>;
  deleteFile(id: string, signal?: AbortSignal): Promise<void>;
  createDownloadToken(id: string, variant?: string, signal?: AbortSignal): Promise<string>;
  downloadBlob(id: string, token: string, variant?: string, signal?: AbortSignal): Promise<Blob>;
}

export interface SystemPort {
  status(signal?: AbortSignal): Promise<SystemStatus>;
  health(signal?: AbortSignal): Promise<SchemaHealth>;
}
export interface AdminSettings { batch: { enabled: boolean; maxRequests: number; maxBodyBytes: number; timeoutMs: number }; logs: { retentionDays: number; level: "debug" | "info" | "warn" | "error"; includeIP: boolean; includeAuth: boolean }; proxy: { trustedProxyCIDRs: string[] }; backups: { schedule: string; timezone: string; enabled: boolean; keep: number }; application: { theme: "system" | "light" | "dark"; accent: string; branding: string }; revision: number }
export interface SettingsPort { get(signal?: AbortSignal): Promise<AdminSettings>; save(value: AdminSettings, signal?: AbortSignal): Promise<AdminSettings>; reload(signal?: AbortSignal): Promise<void> }

export interface ManagedUser { id: string; email: string; active: boolean; verified?: boolean; createdAt: string; updatedAt: string }
export interface OAuthIdentity { provider: string; subject: string; createdAt: string }
export interface UserPage { items: ManagedUser[]; nextAfter?: string }
export interface ImpersonatedUserSession { token: string; user: { id: string; email: string; verified: boolean; createdAt: string }; expiresAt: string; impersonated: true; impersonatingAdministratorId: string }
export interface UsersPort {
  list(after?: string, signal?: AbortSignal): Promise<UserPage>;
  setActive(id: string, active: boolean): Promise<ManagedUser>;
  revokeSessions(id: string): Promise<number>;
  setVerified(id: string, verified: boolean): Promise<ManagedUser>;
  sendVerification(id: string): Promise<void>;
  sendPasswordReset(id: string): Promise<void>;
  impersonate(id: string, durationSeconds?: number): Promise<ImpersonatedUserSession>;
	oauthIdentities?(id: string, signal?: AbortSignal): Promise<OAuthIdentity[]>;
	unlinkOAuthIdentity?(id: string, provider: string, signal?: AbortSignal): Promise<void>;
}

export interface RequestLogEntry { id: number; at: string; method: string; route: string; status: number; durationMs: number }
export interface RequestLogPage { items: RequestLogEntry[]; nextBefore?: number; oldestId: number }
export interface RequestLogFilter { before?: number; statusClass?: number; method?: string; route?: string; from?: string; to?: string }
export interface RequestLogStatistics { total: number; dropped: number; oldestAt?: string; newestAt?: string; averageDurationMs: number; statusClasses: Record<string, number> }
export interface ScheduledJob { id: string; name: string; schedule: string; timezone?: string; enabled: boolean; registered: boolean; running: boolean; nextRunAt: string; lastStartedAt?: string; lastFinishedAt?: string; attempts: number; failed: boolean }
export interface JobPage { items: ScheduledJob[]; workerEnabled: boolean }
export interface BackupEntry { id: string; createdAt: string; size: number; postgresqlMajor: number }
export interface BackupJob { id: string; operation: "create" | "restore"; backupId: string; status: "running" | "completed" | "failed"; phase: string; error?: string; createdAt: string; updatedAt: string }
export interface OperationsPort {
  logs(before?: number, statusClass?: number, signal?: AbortSignal): Promise<RequestLogPage>;
  searchLogs?(filter: RequestLogFilter, signal?: AbortSignal): Promise<RequestLogPage>;
  logDetail?(id: number, signal?: AbortSignal): Promise<RequestLogEntry>;
  logStatistics?(signal?: AbortSignal): Promise<RequestLogStatistics>;
  truncateLogs?(signal?: AbortSignal): Promise<void>;
  jobs(signal?: AbortSignal): Promise<JobPage>;
  configureJob(id: string, schedule: string, enabled: boolean, signal?: AbortSignal): Promise<void>;
  configureJobInTimezone?(id: string, schedule: string, timezone: string, enabled: boolean, signal?: AbortSignal): Promise<void>;
  runJob(id: string, signal?: AbortSignal): Promise<void>;
  // null means this server has no managed backup configuration.
  backups?(signal?: AbortSignal): Promise<BackupEntry[] | null>;
  createBackup?(signal?: AbortSignal): Promise<BackupJob>;
  uploadBackup?(archive: File, signal?: AbortSignal): Promise<BackupEntry>;
  downloadBackup?(id: string, signal?: AbortSignal): Promise<Blob>;
  backupDownloadURL?(id: string): string;
  deleteBackup?(id: string, signal?: AbortSignal): Promise<void>;
  restoreBackup?(id: string, signal?: AbortSignal): Promise<BackupJob>;
  backupJob?(id: string, signal?: AbortSignal): Promise<BackupJob>;
}

export interface AdminBackend {
  storage: StoragePort;
  backupStorage: BackupStoragePort;
  operations?: OperationsPort;
  session: SessionPort;
  schema: SchemaPort;
  records: RecordPort;
  system: SystemPort;
	settings?: SettingsPort;
  users: UsersPort;
  files: FilePort;
  rules: RecordRulesPort;
  authCollections: AuthCollectionsPort;
  serviceTokens: ServiceTokensPort;
}
