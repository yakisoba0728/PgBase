export type FieldType = "boolean" | "integer" | "bigint" | "numeric" | "text" | "timestamptz" | "uuid" | "jsonb" | "relation" | "file" | "editor" | "geoPoint" | "autodate" | "password";
export type FieldOptions = {
  maxSize?: number; convertURLs?: boolean; onCreate?: boolean; onUpdate?: boolean;
  minLength?: number; maxLength?: number; pattern?: string; format?: "email" | "url";
  allowedDomains?: string[]; allowedValues?: string[]; min?: string; max?: string;
  minSelect?: number; maxSelect?: number; maxFileSize?: number; mimeTypes?: string[]; thumbs?: string[];
};
export type RelationInput = { targetCollection: string; cardinality: "single" | "multiple"; minSelect: number; maxSelect: number; onDelete: "restrict" | "cascade" | "set_null" };
export type FieldInput = { id?: string; name: string; type: FieldType; required?: boolean; unique?: boolean; indexed?: boolean; hidden?: boolean; presentable?: boolean; help?: string; default?: unknown; relation?: RelationInput; options?: FieldOptions };
export type FieldDefinition = Omit<FieldInput, "relation"> & { id: string; columnName: string; ordinal: number; relation?: Omit<RelationInput, "targetCollection"> & { targetCollectionId: string } };
export type CollectionDefinition = { id: string; name: string; tableName: string; fields: FieldDefinition[]; indexes?: CompoundIndex[]; view?: ViewInput };
export type CompoundIndex = { id?: string; name: string; fields: string[]; unique: boolean };
export type ViewFilter = { op: "all" | "any" | "eq" | "ne" | "lt" | "lte" | "gt" | "gte"; args?: ViewFilter[]; field?: string; value?: unknown };
export type SQLViewInput = { sql: string; dependencies?: string[]; source?: never; projection?: never; filter?: never };
export type ViewInput = { source: string; projection: string[]; filter?: ViewFilter; sql?: never } | SQLViewInput;
export type CollectionChange =
  | { operation: "create"; name: string; fields?: FieldInput[]; indexes?: CompoundIndex[]; view?: ViewInput }
  | { operation: "update"; name: string; fields: FieldInput[]; indexes?: CompoundIndex[]; view?: never }
  | { operation: "update"; name: string; view: SQLViewInput; fields?: never; indexes?: never }
  | { operation: "delete"; name: string; fields?: never; indexes?: never; view?: never };
export type SchemaPlan = { baseVersion: number; checksum: string; definition: { name: string; tableName: string; fields: Array<FieldInput & { ordinal: number }>; indexes?: CompoundIndex[]; view?: ViewInput }; sql: string[] };
export type SchemaOperation = { operationId: string; status: "running" | "unknown" | "committed" | "failed"; failureCode?: string };
export type SchemaDocumentCollection = { name: string; fields?: FieldInput[]; indexes?: CompoundIndex[]; view?: ViewInput; dependsOn?: string[]; userAccess: boolean; rules: Record<string, unknown> };
export type SchemaDocument = { format: "pgbase.schema/v1"; collections: SchemaDocumentCollection[] };
export type SchemaImportPlan = { baseVersion: number; checksum: string; policyChecksum: string; changes: SchemaPlan[]; document: SchemaDocument; operationId?: string; idempotencyKey?: string };
export type SchemaImportResult = { operationId: string; status: "running" | "unknown" | "committed" | "failed"; version?: number; applied?: number };
export type AdministratorSession = { administrator: { id: string; email: string; createdAt: string }; expiresAt: string };
export type ImpersonatedUserSession = { token: string; user: { id: string; email: string; verified: boolean; createdAt: string }; expiresAt: string; impersonated: true; impersonatingAdministratorId: string };
export type ScheduledJob = { id: string; name: string; schedule: string; enabled: boolean; registered: boolean; running: boolean; nextRunAt: string; lastStartedAt?: string; lastFinishedAt?: string; attempts: number; failed: boolean };
export type RequestLogPage = { items: Array<{ id: number; at: string; method: string; route: string; status: number; durationMs: number }>; nextBefore?: number; oldestId: number };
export type FileStorageSettings = { driver: "local" | "s3"; bucket: string; region: string; endpoint: string; accessKey: string; usePathStyle: boolean; secretConfigured: boolean; revision: number };
export type FileStorageUpdate = { driver: "local" | "s3"; bucket: string; region: string; endpoint: string; accessKey: string; usePathStyle: boolean; revision: number; secretKey?: string; clearSecret?: boolean };
export type FileStorageMigrationInput = Omit<FileStorageUpdate, "revision">;
export type FileStorageMigration = { id: string; status: "preview" | "copying" | "canceling" | "canceled" | "failed" | "ready" | "activating" | "committed"; copiedObjects: number; totalObjects: number; failure?: string; createdAt?: string; updatedAt?: string };
export type BackupStorageSettings = { driver: "local" | "s3"; bucket: string; prefix: string; region: string; endpoint: string; accessKey: string; usePathStyle: boolean; retention: number; secretConfigured: boolean; revision: number };
export type BackupStorageUpdate = Omit<BackupStorageSettings, "secretConfigured"> & { secretKey?: string; clearSecret?: boolean };

export class AdminAPIError extends Error {
  constructor(readonly status: number, readonly code: string, readonly details: Record<string, string[]> = {}) { super(code); this.name = "AdminAPIError"; }
}

/** The preview binds the exact request body and replay identity. Inspect the
 * copy returned by preview; editing it cannot change what apply submits. */
export class ApprovedSchemaPlan {
  readonly #body: string;
  readonly #preview: string;
  readonly #operationID = crypto.randomUUID();
  readonly #idempotencyKey = crypto.randomUUID();
  constructor(change: CollectionChange, plan: SchemaPlan) {
    this.#body = JSON.stringify({ ...change, checksum: plan.checksum, baseVersion: plan.baseVersion });
    this.#preview = JSON.stringify(plan);
  }
  get preview(): SchemaPlan { return JSON.parse(this.#preview) as SchemaPlan; }
  get operationID(): string { return this.#operationID; }
  request(): { body: string; headers: Record<string, string> } {
    return { body: this.#body, headers: { "PgBase-Operation-ID": this.#operationID, "Idempotency-Key": this.#idempotencyKey } };
  }
}

/** A preview plus the exact JSON document that produced it. The apply body is
 * built from this private snapshot so large JSON integers survive preview. */
export class ApprovedSchemaImportPlan implements SchemaImportPlan {
  readonly baseVersion: number;
  readonly checksum: string;
  readonly policyChecksum: string;
  readonly changes: SchemaPlan[];
  readonly document: SchemaDocument;
  readonly operationId = crypto.randomUUID();
  readonly idempotencyKey = crypto.randomUUID();
  readonly #applyBody: string;
  constructor(preview: SchemaImportPlan, documentJSON: string) {
    this.baseVersion = preview.baseVersion;
    this.checksum = preview.checksum;
    this.policyChecksum = preview.policyChecksum;
    this.changes = preview.changes;
    this.document = preview.document;
    this.#applyBody = `{"document":${documentJSON},"baseVersion":${JSON.stringify(preview.baseVersion)},"checksum":${JSON.stringify(preview.checksum)}}`;
  }
  request(): { body: string; headers: Record<string, string> } {
    return { body: this.#applyBody, headers: { "PgBase-Operation-ID": this.operationId, "Idempotency-Key": this.idempotencyKey } };
  }
}

/** Browser administrator client. Cookies are managed by the browser and CSRF
 * tokens stay in memory. Use a same-origin URL; a public user token is a
 * separate identity and is never attached to administrator requests. */
export class PgBaseAdminClient {
  #csrf = "";
  #generation = 0;
  readonly #fetch: typeof fetch;
  constructor(readonly baseURL: string, options: { fetch?: typeof fetch } = {}) { this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis); }

  async login(email: string, password: string, signal?: AbortSignal): Promise<AdministratorSession> {
    return this.adopt("/api/admin/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }, signal);
  }
  resume(signal?: AbortSignal): Promise<AdministratorSession> { return this.adopt("/api/admin/session", {}, signal); }
  async logout(signal?: AbortSignal): Promise<void> {
    const generation = this.#generation;
    await this.request("/api/admin/auth/logout", { method: "POST" }, signal);
    if (generation === this.#generation) { this.#csrf = ""; this.#generation++; }
  }
  catalog(signal?: AbortSignal): Promise<{ version: number; collections: Record<string, CollectionDefinition> }> { return this.request("/api/admin/collections", {}, signal); }
  async plan(change: CollectionChange, signal?: AbortSignal): Promise<ApprovedSchemaPlan> {
    // Snapshot before awaiting: caller edits while the preview runs must not
    // bind a different change to the server's returned checksum.
    const body = JSON.stringify(change);
    const plan = await this.request<SchemaPlan>("/api/admin/collections/plan", { method: "POST", body }, signal);
    return new ApprovedSchemaPlan(JSON.parse(body) as CollectionChange, plan);
  }
  apply(plan: ApprovedSchemaPlan, signal?: AbortSignal): Promise<CollectionDefinition | SchemaOperation | { deleted: boolean; name: string }> {
    return this.request("/api/admin/collections/apply", { method: "POST", ...plan.request() }, signal);
  }
  operation(id: string, signal?: AbortSignal): Promise<SchemaOperation> { return this.request(`/api/admin/schema-operations/${encodeURIComponent(id)}`, {}, signal); }
  exportSchema(signal?: AbortSignal): Promise<SchemaDocument> { return this.request("/api/admin/schema/export", {}, signal); }
  exportSchemaJSON(signal?: AbortSignal): Promise<string> { return this.requestText("/api/admin/schema/export", {}, signal); }
  async previewSchemaImport(document: SchemaDocument, signal?: AbortSignal): Promise<ApprovedSchemaImportPlan> {
    const raw = JSON.stringify(document); return new ApprovedSchemaImportPlan(await this.request<SchemaImportPlan>("/api/admin/schema/import/preview", { method: "POST", body: raw }, signal), raw);
  }
  async previewSchemaImportJSON(documentJSON: string, signal?: AbortSignal): Promise<ApprovedSchemaImportPlan> {
    return new ApprovedSchemaImportPlan(await this.request<SchemaImportPlan>("/api/admin/schema/import/preview", { method: "POST", body: documentJSON }, signal), documentJSON);
  }
  applySchemaImport(plan: SchemaImportPlan, signal?: AbortSignal): Promise<SchemaImportResult> {
    const request = plan instanceof ApprovedSchemaImportPlan ? plan.request() : { headers: { "PgBase-Operation-ID": plan.operationId ?? "", "Idempotency-Key": plan.idempotencyKey ?? "" }, body: JSON.stringify({ document: plan.document, baseVersion: plan.baseVersion, checksum: plan.checksum }) };
    return this.request("/api/admin/schema/import/apply", { method: "POST", ...request }, signal);
  }
  logs(options: { before?: number; statusClass?: number } = {}, signal?: AbortSignal): Promise<RequestLogPage> {
    const query = new URLSearchParams();
    if (options.before !== undefined) query.set("before", String(options.before));
    if (options.statusClass !== undefined) query.set("statusClass", String(options.statusClass));
    return this.request(`/api/admin/logs?${query}`, {}, signal);
  }
  jobs(signal?: AbortSignal): Promise<{ items: ScheduledJob[]; workerEnabled: boolean }> { return this.request("/api/admin/jobs", {}, signal); }
  configureJob(id: string, schedule: string, enabled: boolean, signal?: AbortSignal): Promise<void> { return this.request(`/api/admin/jobs/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify({ schedule, enabled }) }, signal); }
  queueJob(id: string, signal?: AbortSignal): Promise<void> { return this.request(`/api/admin/jobs/${encodeURIComponent(id)}/run`, { method: "POST" }, signal); }
  fileStorageSettings(signal?: AbortSignal): Promise<FileStorageSettings> { return this.request("/api/admin/storage/files", {}, signal); }
  updateFileStorage(input: FileStorageUpdate, signal?: AbortSignal): Promise<FileStorageSettings> { return this.request("/api/admin/storage/files", { method: "PUT", body: JSON.stringify(input) }, signal); }
  async testFileStorage(signal?: AbortSignal): Promise<void> { await this.request("/api/admin/storage/files/test", { method: "POST" }, signal); }
  backupStorageSettings(signal?: AbortSignal): Promise<BackupStorageSettings> { return this.request("/api/admin/storage/backups", {}, signal); }
  updateBackupStorage(input: BackupStorageUpdate, signal?: AbortSignal): Promise<BackupStorageSettings> { return this.request("/api/admin/storage/backups", { method: "PUT", body: JSON.stringify(input) }, signal); }
  async testBackupStorage(signal?: AbortSignal): Promise<void> { await this.request("/api/admin/storage/backups/test", { method: "POST" }, signal); }
  previewFileStorageMigration(input: FileStorageMigrationInput, signal?: AbortSignal): Promise<FileStorageMigration> { return this.request("/api/admin/storage/files/migrations/preview", { method: "POST", body: JSON.stringify(input) }, signal); }
  startFileStorageMigration(input: FileStorageMigrationInput, operationID: string, signal?: AbortSignal): Promise<FileStorageMigration> { return this.request("/api/admin/storage/files/migrations", { method: "POST", body: JSON.stringify(input), headers: { "PgBase-Operation-ID": operationID } }, signal); }
  fileStorageMigration(id: string, signal?: AbortSignal): Promise<FileStorageMigration> { return this.request(`/api/admin/storage/files/migrations/${encodeURIComponent(id)}`, {}, signal); }
  latestFileStorageMigration(signal?: AbortSignal): Promise<FileStorageMigration> { return this.request("/api/admin/storage/files/migrations/latest", {}, signal); }
  async resumeFileStorageMigration(id: string, signal?: AbortSignal): Promise<void> { await this.request(`/api/admin/storage/files/migrations/${encodeURIComponent(id)}/resume`, { method: "POST" }, signal); }
  async cancelFileStorageMigration(id: string, signal?: AbortSignal): Promise<void> { await this.request(`/api/admin/storage/files/migrations/${encodeURIComponent(id)}/cancel`, { method: "POST" }, signal); }
  impersonateUser(id: string, durationSeconds = 300, signal?: AbortSignal): Promise<ImpersonatedUserSession> {
    return this.request(`/api/admin/users/${encodeURIComponent(id)}/impersonate`, { method: "POST", body: JSON.stringify({ durationSeconds }) }, signal);
  }

  private async adopt(path: string, init: RequestInit, signal?: AbortSignal): Promise<AdministratorSession> {
    const generation = ++this.#generation;
    const session = await this.request<AdministratorSession & { csrfToken: string }>(path, init, signal, true);
    if (!session.csrfToken) throw new AdminAPIError(0, "invalid_session_response");
    if (generation === this.#generation) this.#csrf = session.csrfToken;
    return { administrator: session.administrator, expiresAt: session.expiresAt };
  }
  private async request<T>(path: string, init: RequestInit, signal?: AbortSignal, anonymous = false): Promise<T> {
    const generation = this.#generation;
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body !== undefined) headers.set("Content-Type", "application/json");
    if (!anonymous && init.method && init.method !== "GET") headers.set("X-PgBase-CSRF", this.#csrf);
    const response = await this.#fetch(new URL(path, this.baseURL), { ...init, headers, credentials: "include", signal });
    if (!response.ok) {
      if (response.status === 401 && generation === this.#generation) { this.#csrf = ""; this.#generation++; }
      const data = await response.json().catch(() => undefined) as { error?: { code?: string; details?: Record<string, string[]> } } | undefined;
      throw new AdminAPIError(response.status, data?.error?.code ?? "request_failed", data?.error?.details);
    }
    if (response.status === 204) return undefined as T;
    try { return await response.json() as T; } catch { throw new AdminAPIError(response.status, "invalid_response_body"); }
  }
  private async requestText(path: string, init: RequestInit, signal?: AbortSignal): Promise<string> {
    const headers = new Headers(init.headers); headers.set("Accept", "application/json");
    if (init.body !== undefined) headers.set("Content-Type", "application/json");
    if (init.method && init.method !== "GET") headers.set("X-PgBase-CSRF", this.#csrf);
    const response = await this.#fetch(new URL(path, this.baseURL), { ...init, headers, credentials:"include", signal });
    const text = await response.text();
    if (!response.ok) throw new AdminAPIError(response.status, "request_failed");
    return text;
  }
}
