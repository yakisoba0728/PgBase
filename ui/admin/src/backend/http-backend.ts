import { StorageMigrationNotFound, StorageMigrationRejected } from "./storage-port";
import { parseJSON, stringifyJSON } from "./json";
import type { FileStorageMigration, FileStorageMigrationInput, FileStorageSettings, FileStorageUpdate } from "./storage-port";
import type { BackupStorageSettings, BackupStorageUpdate } from "./backup-storage-port";
import { providerInput, smtpInput, templateInput } from "./auth-input";
import type { SQLConsoleTransport } from "./sql-console-backend";
import {
  CollectionCatalog,
  PlannedChange,
  RecordError,
  SchemaError,
  SessionError,
  VersionedRecord,
  type AdminBackend,
  type RequestLogPage, type RequestLogEntry, type RequestLogFilter, type RequestLogStatistics, type JobPage, type BackupEntry, type BackupJob,
  type ApplyAttempt,
  type ApplyOutcome,
  type AuthenticatedSession,
  type CollectionChange,
  type CollectionDefinition,
	 type FieldDefinition,
  type CreateAttempt,
  type ListOptions,
  type OperationHistory,
  type OperationReceipt,
  type OperationStatus,
  type PlannedField,
  type LoginInput,
  type RecordData,
  type RecordPage,
  type ResumedSession,
  type SchemaHealth,
  type SetupInput,
	 type SetupBackup, type SetupBackupJob,
  type SystemStatus,
	 type AdminSettings,
  type ManagedUser,
	 type OAuthIdentity,
  type UserPage,
  type ImpersonatedUserSession,
  type FileInfo,
  type RecordRules,
  type RulePreview,
  type AuthCollection,
  type AuthAdministration, type AuthPolicy, type OAuthProviderSettings, type AuthMailTemplate, type AuthMailPreview, type AuthSMTPSettings,
  type IssuedServiceToken,
  type ServiceToken, type ServiceTokenScope,
	type SchemaDocument, type SchemaImportPlan, type SchemaImportResult,
} from "./ports";

interface Wire {
  status: number;
  code: string;
  details: Record<string, string[]>;
}

class WireError extends Error {
  constructor(readonly wire: Wire) {
    super(wire.code);
    this.name = "WireError";
  }
}

interface Request {
  path: string;
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  csrf?: boolean;
  anonymous?: boolean;
}

interface Reply {
  data: unknown;
  headers: Headers;
  status: number;
}

function envelope(body: unknown): Omit<Wire, "status"> | null {
  if (typeof body !== "object" || body === null || !("error" in body)) return null;
  const error = body.error as { code?: unknown; message?: unknown; details?: Record<string, string[]> };
  if (typeof error.code !== "string" || typeof error.message !== "string") return null;
  return { code: error.code, details: error.details ?? {} };
}

async function decode(response: Response): Promise<unknown> {
  const text = await response.text();
  return text.trim() === "" ? undefined : parseJSON(text);
}

// A strong ETag is the only safe precondition for the next write, so a record
// response without one is a failed read rather than a record with no version.
function strongETag(headers: Headers): string | null {
  const etag = headers.get("ETag")?.trim();
  if (etag === undefined || etag.length < 2 || etag[0] !== '"' || etag[etag.length - 1] !== '"') return null;
  for (let index = 1; index < etag.length - 1; index += 1) {
    const code = etag.charCodeAt(index);
    if (code !== 0x21 && !(code >= 0x23 && code <= 0x7e) && !(code >= 0x80 && code <= 0xff)) return null;
  }
  return etag;
}

// The bounded page the ledger serves by default. The UI asks for exactly this
// and nothing else, so no request can reach the server's 100-item ceiling.
const historyPageSize = 20;
const maximumHistoryPageSize = 100;
const operationsPath = "/api/admin/schema-operations";

/** Rebuilds only contract fields so a response cannot smuggle raw SQL, request hashes or secrets into the UI. */
function receipt(raw: unknown): OperationReceipt {
  const wire = raw as Record<string, unknown>;
  const bounded: OperationReceipt = {
    operationId: String(wire.operationId ?? ""),
    operation: String(wire.operation ?? ""),
    collectionId: String(wire.collectionId ?? ""),
    status: wire.status as OperationStatus,
    baseVersion: Number(wire.baseVersion ?? 0),
    createdAt: String(wire.createdAt ?? ""),
    updatedAt: String(wire.updatedAt ?? ""),
  };
  if (wire.committedVersion !== undefined) bounded.committedVersion = Number(wire.committedVersion);
  if (wire.failureCode !== undefined) bounded.failureCode = String(wire.failureCode);
  if (wire.completedAt !== undefined) bounded.completedAt = String(wire.completedAt);
  return bounded;
}

// A guarded apply that answers with a collection body committed, and that body
// is the collection the change produced. Every other 2xx is the ledger
// speaking, and only a committed receipt is a success: a replayed, recovered or
// still-running operation stays undecided, and a committed receipt carries no
// collection. A delete answers with neither, so the field list is what tells a
// collection body apart from one.
function applyOutcome(data: unknown, operationId: string): ApplyOutcome {
  const body = data as { status?: unknown; fields?: unknown } | undefined;
  if (typeof body?.status !== "string") {
    return Array.isArray(body?.fields) ? { kind: "applied", definition: data as CollectionDefinition } : { kind: "applied" };
  }
  return body.status === "committed" ? { kind: "applied" } : { kind: "undecided", operationId };
}

function recordPath(collection: string, id?: string): string {
  const base = `/api/v1/collections/${encodeURIComponent(collection)}/records`;
  return id === undefined ? base : `${base}/${encodeURIComponent(id)}`;
}

function fileIdentity(files: Record<string, File[]>): Record<string, string[]> {
	return Object.fromEntries(Object.entries(files).map(([field, selected]) => [field, selected.map((file) => `${file.name}:${file.size}:${file.type}:${file.lastModified}`)]));
}

function definitions(raw: unknown): CollectionDefinition[] {
  const collections = (raw as { collections?: Record<string, CollectionDefinition> }).collections ?? {};
  return Object.values(collections);
}

function recordFailure(wire: Wire): RecordError {
  if (wire.status === 404) return new RecordError("not-found");
  if (wire.status === 412) return new RecordError("stale");
  if (wire.code === "validation_failed") return new RecordError("rejected", wire.details);
  return new RecordError("failed");
}

function sessionFailure(wire: Wire): SessionError {
  if (wire.code === "invalid_credentials") return new SessionError("invalid-credentials");
	if (wire.code === "otp_required") return new SessionError("otp-required");
  if (wire.code === "setup_unavailable") return new SessionError("setup-unavailable");
  if (wire.code === "validation_failed") return new SessionError("invalid-input");
  return new SessionError("failed");
}

class HTTPBackend implements AdminBackend {
  #csrfToken = "";
  #authGeneration = 0;
  readonly #expired = new Set<() => void>();

  readonly session = {
    resume: async (signal?: AbortSignal): Promise<ResumedSession> => {
      try {
        return { kind: "authenticated", session: this.#adopt(await this.#send({ path: "/api/admin/session", signal, anonymous: true })) };
      } catch (caught: unknown) {
        if (!(caught instanceof WireError)) throw caught;
        if (caught.wire.status === 404 && caught.wire.code === "setup_required") return { kind: "setup-required" };
        if (caught.wire.status !== 401) return { kind: "unreachable" };
        try {
          const status = await this.#send({ path: "/api/admin/status", signal, anonymous: true });
          return (status.data as { setupRequired: boolean }).setupRequired ? { kind: "setup-required" } : { kind: "anonymous" };
        } catch (probe: unknown) {
          if (signal?.aborted) throw probe;
          return { kind: "setup-unknown" };
        }
      }
    },
    setup: (input: SetupInput) => this.#authenticate("/api/admin/setup", input),
    login: (input: LoginInput) => this.#authenticate("/api/admin/auth/login", input),
    logout: async (): Promise<void> => {
      const authGeneration = this.#authGeneration;
      try {
        await this.#send({
          path: "/api/admin/auth/logout",
          method: "POST",
          headers: { "X-CSRF-Token": this.#csrfToken, "X-PgBase-CSRF": this.#csrfToken },
        });
      } catch (caught: unknown) {
        throw caught instanceof WireError ? sessionFailure(caught.wire) : caught;
      }
      if (authGeneration === this.#authGeneration) { this.#csrfToken = ""; this.#authGeneration += 1; }
    },
		uploadSetupBackup: async (setupToken: string, archive: File, signal?: AbortSignal): Promise<SetupBackup> => {
			const response = await fetch("/api/admin/setup/backups/upload", { method: "POST", body: archive, credentials: "include", signal, headers: { "Content-Type": "application/gzip", "X-PgBase-Setup-Token": setupToken } });
			if (!response.ok) throw new SessionError(response.status === 401 ? "invalid-credentials" : "failed");
			return await decode(response) as SetupBackup;
		},
		restoreSetupBackup: async (setupToken: string, id: string, signal?: AbortSignal): Promise<SetupBackupJob> => {
			try { return (await this.#send({ path: `/api/admin/setup/backups/${encodeURIComponent(id)}/restore`, method: "POST", headers: { "X-PgBase-Setup-Token": setupToken }, anonymous: true, signal })).data as SetupBackupJob; }
			catch (error) { throw error instanceof WireError && error.wire.status === 401 ? new SessionError("invalid-credentials") : error; }
		},
		setupBackupJob: async (setupToken: string, id: string, signal?: AbortSignal): Promise<SetupBackupJob> => {
			const response = await fetch(`/api/admin/setup/backup-jobs/${encodeURIComponent(id)}`, { credentials: "include", signal, headers: { "X-PgBase-Setup-Token": setupToken } });
			if (!response.ok) throw new SessionError(response.status === 401 ? "invalid-credentials" : "failed"); return await decode(response) as SetupBackupJob;
		},
		requestPasswordReset: async (email: string, signal?: AbortSignal): Promise<void> => { await this.#send({ path: "/api/admin/auth/request-password-reset", method: "POST", body: { email }, anonymous: true, signal }); },
		confirmPasswordReset: async (token: string, newPassword: string, signal?: AbortSignal): Promise<void> => { await this.#send({ path: "/api/admin/auth/confirm-password-reset", method: "POST", body: { token, newPassword }, anonymous: true, signal }); },
    onExpired: (listener: () => void) => {
      this.#expired.add(listener);
      return () => { this.#expired.delete(listener); };
    },
  };

  readonly schema = {
    catalog: async (signal?: AbortSignal): Promise<CollectionCatalog> => {
      const reply = await this.#schemaCall({ path: "/api/admin/collections", signal });
      return new CollectionCatalog((reply.data as { version: number }).version, definitions(reply.data));
    },
    plan: async (change: CollectionChange, signal?: AbortSignal): Promise<PlannedChange> => {
      const reply = await this.#schemaCall({ path: "/api/admin/collections/plan", method: "POST", body: change, csrf: true, signal });
      const plan = reply.data as { baseVersion: number; checksum: string; definition: { fields: PlannedField[] }; sql: string[] };
      return new PlannedChange(change, plan.checksum, plan.baseVersion, plan.definition.fields, Array.isArray(plan.sql) ? plan.sql : []);
    },
    apply: async (plan: PlannedChange, attempt: ApplyAttempt): Promise<ApplyOutcome> => {
      const identity = attempt.identityFor(plan);
      try {
        const reply = await this.#send({
          path: "/api/admin/collections/apply",
          method: "POST",
          body: plan.applyRequest(),
          csrf: true,
          headers: { "PgBase-Operation-ID": identity.operationId, "Idempotency-Key": identity.idempotencyKey },
        });
        return applyOutcome(reply.data, identity.operationId);
      } catch (caught: unknown) {
        // A request that never answered says nothing about the transaction: it
        // races the commit rather than reporting on it. A 2xx
        // whose body could not be read is the same case — the server accepted
        // the apply and only the answer was lost. The identity was minted
        // before the request precisely so both can be asked about.
        if (!(caught instanceof WireError) || caught.wire.code === "invalid_response_body") {
          return { kind: "undecided", operationId: identity.operationId };
        }
        throw await this.#applyFailure(caught, plan);
      }
    },
    operation: async (operationId: string, signal?: AbortSignal): Promise<OperationReceipt> => {
      const path = `${operationsPath}/${encodeURIComponent(operationId)}`;
      return receipt((await this.#schemaCall({ path, signal })).data);
    },
    operations: async (cursor?: string, signal?: AbortSignal): Promise<OperationHistory> => {
      const query = new URLSearchParams({ limit: String(historyPageSize) });
      if (cursor !== undefined && cursor !== "") query.set("cursor", cursor);
      const page = (await this.#schemaCall({ path: `${operationsPath}?${query}`, signal })).data as OperationHistory;
      // A page above the ledger's own ceiling is not a page this client asked
      // for, so it is a failed read rather than an unbounded render.
      if (page.items.length > maximumHistoryPageSize) throw new SchemaError("failed", "invalid_response_body");
      return { items: page.items.map(receipt), ...(page.nextCursor === undefined ? {} : { nextCursor: page.nextCursor }) };
    },
    userAccess: async (collection: string, signal?: AbortSignal) => (await this.#schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/user-access`, signal })).data as { collection: string; enabled: boolean },
    setUserAccess: async (collection: string, enabled: boolean) => (await this.#schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/user-access`, method: "PUT", body: { enabled }, csrf: true })).data as { collection: string; enabled: boolean },
		exportDocument: async (signal?: AbortSignal) => (await this.#schemaCall({ path: "/api/admin/schema/export", signal })).data as SchemaDocument,
		previewImport: async (document: SchemaDocument, signal?: AbortSignal) => ({ ...((await this.#schemaCall({ path: "/api/admin/schema/import/preview", method: "POST", body: document, csrf: true, signal })).data as SchemaImportPlan), operationId: crypto.randomUUID(), idempotencyKey: crypto.randomUUID() }),
		applyImport: async (plan: SchemaImportPlan, signal?: AbortSignal) => {
			try { return (await this.#schemaCall({ path: "/api/admin/schema/import/apply", method: "POST", body: { document: plan.document, baseVersion: plan.baseVersion, checksum: plan.checksum }, headers: { "PgBase-Operation-ID": plan.operationId ?? "", "Idempotency-Key": plan.idempotencyKey ?? "" }, csrf: true, signal })).data as SchemaImportResult; }
			catch (caught) { if (plan.operationId && (!(caught instanceof WireError) || caught.wire.code === "invalid_response_body")) return (await this.#schemaCall({ path:`/api/admin/schema/import/operations/${encodeURIComponent(plan.operationId)}`, signal })).data as SchemaImportResult; throw caught; }
		},
		sampleView: async (query: string, signal?: AbortSignal) => (await this.#schemaCall({ path: "/api/admin/collections/view/sample", method: "POST", body: { query }, csrf: true, signal })).data as { fields: FieldDefinition[]; items: RecordData[]; dependencies: string[] },
		importStatus: async (operationId: string, signal?: AbortSignal) => (await this.#schemaCall({ path:`/api/admin/schema/import/operations/${encodeURIComponent(operationId)}`, signal })).data as SchemaImportResult,
  };

  readonly records = {
    list: async (collection: string, options: ListOptions, signal?: AbortSignal): Promise<RecordPage> => {
      const query = new URLSearchParams({ limit: "50" });
      if (options.cursor) query.set("cursor", options.cursor);
      if (options.filter) query.set("filter", options.filter);
      if (options.sort) query.set("sort", options.sort);
      return (await this.#recordCall({ path: `${recordPath(collection)}?${query}`, signal })).data as RecordPage;
    },
    get: async (collection: string, id: string, signal?: AbortSignal): Promise<VersionedRecord> => {
      return this.#versioned(collection, id, await this.#recordCall({ path: recordPath(collection, id), signal }));
    },
		create: async (attempt: CreateAttempt, input: RecordData): Promise<VersionedRecord> => {
      const reply = await this.#recordCall({
        path: recordPath(attempt.collection),
        method: "POST",
        body: input,
        csrf: true,
        headers: { "Idempotency-Key": attempt.keyFor(input) },
      });
			return this.#versioned(attempt.collection, String((reply.data as RecordData).id ?? ""), reply);
		},
		createWithFiles: async (attempt: CreateAttempt, input: RecordData, files: Record<string, File[]>): Promise<VersionedRecord> =>
			this.#multipartRecord(attempt.collection, undefined, input, files, { "Idempotency-Key": attempt.keyFor({ ...input, $files: fileIdentity(files) }) }),
		update: async (record: VersionedRecord, patch: RecordData): Promise<VersionedRecord> => {
      const reply = await this.#recordCall({
        path: recordPath(record.collection, record.id),
        method: "PATCH",
        body: patch,
        csrf: true,
        headers: { "If-Match": record.precondition() },
      });
			return this.#versioned(record.collection, record.id, reply);
		},
		updateWithFiles: async (record: VersionedRecord, patch: RecordData, files: Record<string, File[]>): Promise<VersionedRecord> =>
			this.#multipartRecord(record.collection, record.id, patch, files, { "If-Match": record.precondition() }),
    remove: async (record: VersionedRecord): Promise<void> => {
      await this.#recordCall({
        path: recordPath(record.collection, record.id),
        method: "DELETE",
        csrf: true,
        headers: { "If-Match": record.precondition() },
      });
    },
  };

  readonly system = {
    status: async (signal?: AbortSignal): Promise<SystemStatus> => {
      try {
        return (await this.#send({ path: "/api/admin/system", signal })).data as SystemStatus;
      } catch (caught: unknown) {
        throw this.#systemFailure(caught);
      }
    },
    health: async (signal?: AbortSignal): Promise<SchemaHealth> => {
      try {
        return (await this.#send({ path: "/api/admin/system/health", signal })).data as SchemaHealth;
      } catch (caught: unknown) {
        throw this.#systemFailure(caught);
      }
    },
  };

  readonly settings = {
    get: async (signal?: AbortSignal): Promise<AdminSettings> => (await this.#send({ path: "/api/admin/settings", signal })).data as AdminSettings,
    save: async (value: AdminSettings, signal?: AbortSignal): Promise<AdminSettings> => (await this.#send({ path: "/api/admin/settings", method: "PATCH", body: value, csrf: true, signal })).data as AdminSettings,
    reload: async (signal?: AbortSignal): Promise<void> => { await this.#send({ path: "/api/admin/settings/reload", method: "POST", csrf: true, signal }); },
  };

  readonly operations = {
    logs: async (before?: number, statusClass?: number, signal?: AbortSignal): Promise<RequestLogPage> => {
      const query = new URLSearchParams();
      if (before !== undefined) query.set("before", String(before));
      if (statusClass !== undefined) query.set("statusClass", String(statusClass));
      return (await this.#send({ path: `/api/admin/logs?${query}`, signal })).data as RequestLogPage;
    },
    searchLogs: async (filter: RequestLogFilter, signal?: AbortSignal): Promise<RequestLogPage> => {
      const query = new URLSearchParams();
      for (const [key, value] of Object.entries(filter)) if (value !== undefined && value !== "") query.set(key, String(value));
      return (await this.#send({ path: `/api/admin/logs?${query}`, signal })).data as RequestLogPage;
    },
    logDetail: async (id: number, signal?: AbortSignal): Promise<RequestLogEntry> =>
      (await this.#send({ path: `/api/admin/logs/${encodeURIComponent(id)}`, signal })).data as RequestLogEntry,
    logStatistics: async (signal?: AbortSignal): Promise<RequestLogStatistics> =>
      (await this.#send({ path: "/api/admin/logs/statistics", signal })).data as RequestLogStatistics,
    truncateLogs: async (signal?: AbortSignal): Promise<void> => {
      await this.#send({ path: "/api/admin/logs", method: "DELETE", csrf: true, signal });
    },
    jobs: async (signal?: AbortSignal): Promise<JobPage> => (await this.#send({ path: "/api/admin/jobs", signal })).data as JobPage,
    configureJob: async (id: string, schedule: string, enabled: boolean, signal?: AbortSignal): Promise<void> => {
      await this.#send({ path: `/api/admin/jobs/${encodeURIComponent(id)}`, method: "PUT", body: { schedule, enabled }, csrf: true, signal });
    },
    configureJobInTimezone: async (id: string, schedule: string, timezone: string, enabled: boolean, signal?: AbortSignal): Promise<void> => {
      await this.#send({ path: `/api/admin/jobs/${encodeURIComponent(id)}`, method: "PUT", body: { schedule, timezone, enabled }, csrf: true, signal });
    },
    runJob: async (id: string, signal?: AbortSignal): Promise<void> => {
      await this.#send({ path: `/api/admin/jobs/${encodeURIComponent(id)}/run`, method: "POST", csrf: true, signal });
    },
    backups: async (signal?: AbortSignal): Promise<BackupEntry[] | null> => {
      try { return ((await this.#send({ path: "/api/admin/backups", signal })).data as { items: BackupEntry[] }).items; }
      catch (error) { if (error instanceof WireError && error.wire.status === 404) return null; throw this.#systemFailure(error); }
    },
    createBackup: async (signal?: AbortSignal): Promise<BackupJob> => (await this.#send({ path: "/api/admin/backups", method: "POST", csrf: true, signal })).data as BackupJob,
    uploadBackup: async (archive: File, signal?: AbortSignal): Promise<BackupEntry> => {
      const response = await fetch("/api/admin/backups/upload", { method: "POST", body: archive, headers: { "Content-Type": "application/gzip", "X-PgBase-CSRF": this.#csrfToken }, credentials: "include", signal });
      if (!response.ok) throw new Error(`backup_upload_${response.status}`);
      return await decode(response) as BackupEntry;
    },
    backupDownloadURL: (id: string): string => `/api/admin/backups/${encodeURIComponent(id)}`,
    downloadBackup: async (id: string, signal?: AbortSignal): Promise<Blob> => {
      const response = await fetch(`/api/admin/backups/${encodeURIComponent(id)}`, { credentials: "include", signal });
      if (!response.ok) throw new Error(`backup_download_${response.status}`);
      return response.blob();
    },
    deleteBackup: async (id: string, signal?: AbortSignal): Promise<void> => { await this.#send({ path: `/api/admin/backups/${encodeURIComponent(id)}`, method: "DELETE", csrf: true, signal }); },
    restoreBackup: async (id: string, signal?: AbortSignal): Promise<BackupJob> => (await this.#send({ path: `/api/admin/backups/${encodeURIComponent(id)}/restore`, method: "POST", csrf: true, signal })).data as BackupJob,
    backupJob: async (id: string, signal?: AbortSignal): Promise<BackupJob> => (await this.#send({ path: `/api/admin/backup-jobs/${encodeURIComponent(id)}`, signal })).data as BackupJob,
  };

  readonly storage = {
    getFiles: async (signal?: AbortSignal): Promise<FileStorageSettings> => (await this.#send({ path: "/api/admin/storage/files", signal })).data as FileStorageSettings,
    saveFiles: async (input: FileStorageUpdate, signal?: AbortSignal): Promise<FileStorageSettings> => (await this.#send({ path: "/api/admin/storage/files", method: "PUT", body: input, csrf: true, signal })).data as FileStorageSettings,
    testFiles: async (signal?: AbortSignal): Promise<void> => { await this.#send({ path: "/api/admin/storage/files/test", method: "POST", csrf: true, signal }); },
    previewMigration: async (input: FileStorageMigrationInput, signal?: AbortSignal): Promise<FileStorageMigration> => (await this.#send({ path: "/api/admin/storage/files/migrations/preview", method: "POST", body: input, csrf: true, signal })).data as FileStorageMigration,
    startMigration: async (input: FileStorageMigrationInput, operationID: string, signal?: AbortSignal): Promise<FileStorageMigration> => {
      try { return (await this.#send({ path: "/api/admin/storage/files/migrations", method: "POST", body: input, headers: { "PgBase-Operation-ID": operationID }, csrf: true, signal })).data as FileStorageMigration; }
      catch (error) { if (error instanceof WireError && error.wire.status >= 400 && error.wire.status < 500) throw new StorageMigrationRejected(error.wire.code); throw this.#systemFailure(error); }
    },
    migration: async (id: string, signal?: AbortSignal): Promise<FileStorageMigration> => {
      try { return (await this.#send({ path: `/api/admin/storage/files/migrations/${encodeURIComponent(id)}`, signal })).data as FileStorageMigration; }
      catch (error) { if (error instanceof WireError && error.wire.status === 404) throw new StorageMigrationNotFound(); throw this.#systemFailure(error); }
    },
    latestMigration: async (signal?: AbortSignal): Promise<FileStorageMigration | null> => {
      try { return (await this.#send({ path: "/api/admin/storage/files/migrations/latest", signal })).data as FileStorageMigration; }
      catch (error) { if (error instanceof WireError && error.wire.status === 404) return null; throw this.#systemFailure(error); }
    },
    resumeMigration: async (id: string, signal?: AbortSignal): Promise<void> => { await this.#send({ path: `/api/admin/storage/files/migrations/${encodeURIComponent(id)}/resume`, method: "POST", csrf: true, signal }); },
    cancelMigration: async (id: string, signal?: AbortSignal): Promise<void> => { await this.#send({ path: `/api/admin/storage/files/migrations/${encodeURIComponent(id)}/cancel`, method: "POST", csrf: true, signal }); },
  };

  readonly backupStorage = {
    get: async (signal?:AbortSignal):Promise<BackupStorageSettings> => (await this.#send({path:"/api/admin/storage/backups",signal})).data as BackupStorageSettings,
    save: async (input:BackupStorageUpdate,signal?:AbortSignal):Promise<BackupStorageSettings> => (await this.#send({path:"/api/admin/storage/backups",method:"PUT",body:input,csrf:true,signal})).data as BackupStorageSettings,
    test: async (signal?:AbortSignal):Promise<void> => { await this.#send({path:"/api/admin/storage/backups/test",method:"POST",csrf:true,signal}); },
  };

  readonly users = {
    list: async (after?: string, signal?: AbortSignal): Promise<UserPage> => {
      const query = new URLSearchParams({ limit: "50" });
      if (after) query.set("after", after);
      return (await this.#send({ path: `/api/admin/users?${query}`, signal })).data as UserPage;
    },
    setActive: async (id: string, active: boolean): Promise<ManagedUser> =>
      (await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}`, method: "PATCH", body: { active }, csrf: true })).data as ManagedUser,
    revokeSessions: async (id: string): Promise<number> =>
      Number(((await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}/revoke-sessions`, method: "POST", csrf: true })).data as { revokedSessions: number }).revokedSessions),
    setVerified: async (id: string, verified: boolean): Promise<ManagedUser> =>
      (await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}/verification`, method: "PUT", body: { verified }, csrf: true })).data as ManagedUser,
    sendVerification: async (id: string): Promise<void> => { await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}/send-verification`, method: "POST", csrf: true }); },
    sendPasswordReset: async (id: string): Promise<void> => { await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}/send-password-reset`, method: "POST", csrf: true }); },
    impersonate: async (id: string, durationSeconds = 300) =>
      (await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}/impersonate`, method: "POST", body: { durationSeconds }, csrf: true })).data as ImpersonatedUserSession,
		oauthIdentities: async (id: string, signal?: AbortSignal): Promise<OAuthIdentity[]> => ((await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}/oauth-identities`, signal })).data as { items: OAuthIdentity[] }).items,
		unlinkOAuthIdentity: async (id: string, provider: string, signal?: AbortSignal): Promise<void> => { await this.#send({ path: `/api/admin/users/${encodeURIComponent(id)}/oauth-identities/${encodeURIComponent(provider)}`, method: "DELETE", csrf: true, signal }); },
  };

  readonly files = {
    listFiles: async (collection: string, recordID: string, field: string, signal?: AbortSignal): Promise<FileInfo[]> => {
      const reply = await this.#recordCall({ path: this.#fieldFilePath(collection, recordID, field), signal });
      return (reply.data as { items: FileInfo[] }).items;
    },
    uploadFile: async (collection: string, recordID: string, field: string, file: File, signal?: AbortSignal): Promise<FileInfo> => {
      const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", await file.arrayBuffer()));
      const body = new FormData();
      body.append("file", file, file.name);
      return this.#fileCall(this.#fieldFilePath(collection, recordID, field), { method: "POST", body, headers: {
        "X-PgBase-CSRF": this.#csrfToken,
        "X-PgBase-File-Size": String(file.size),
        "X-PgBase-File-SHA256": Array.from(digest, byte => byte.toString(16).padStart(2, "0")).join(""),
      }, signal });
    },
    deleteFile: async (id: string, signal?: AbortSignal): Promise<void> => {
      await this.#fileCall(this.#filePath(id), { method: "DELETE", headers: { "X-PgBase-CSRF": this.#csrfToken }, signal });
    },
    createDownloadToken: async (id: string, variant = "original", signal?: AbortSignal): Promise<string> =>
      (await this.#fileCall(this.#filePath(id) + "/token", { method: "POST", body: { variant }, csrf: true, signal }) as { token: string }).token,
    downloadBlob: async (id: string, token: string, variant = "original", signal?: AbortSignal): Promise<Blob> => {
      const path = new URL(this.#filePath(id), window.location.origin);
      path.searchParams.set("token", token); if (variant !== "original") path.searchParams.set("variant", variant);
      const response = await fetch(path, { credentials: "include", signal });
      if (!response.ok) throw new RecordError(response.status === 404 ? "not-found" : "failed");
      return response.blob();
    },
  };
  readonly rules = {
    getRules: async (collection: string, signal?: AbortSignal): Promise<RecordRules> =>
      (await this.#schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/rules`, signal })).data as RecordRules,
    setRules: async (collection: string, rules: Record<string, unknown>, revision: number, signal?: AbortSignal): Promise<RecordRules> =>
      (await this.#schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/rules`, method: "PUT", body: { rules, revision }, csrf: true, signal })).data as RecordRules,
    previewRule: async (collection: string, value: { rule: Record<string, unknown>; action: "list" | "view" | "update" | "delete"; userId?: string; recordIds: string[] }, signal?: AbortSignal): Promise<RulePreview> =>
      (await this.#schemaCall({ path: `/api/admin/collections/${encodeURIComponent(collection)}/rules/preview`, method: "POST", body: value, csrf: true, signal })).data as RulePreview,
  };
  readonly authCollections = {
    listAuthCollections: async (signal?: AbortSignal): Promise<AuthCollection[]> =>
      ((await this.#schemaCall({ path: "/api/admin/auth-collections", signal })).data as { items: AuthCollection[] }).items,
    createAuthCollection: async (value: AuthCollection, signal?: AbortSignal): Promise<AuthCollection> =>
      (await this.#schemaCall({ path: "/api/admin/auth-collections", method: "POST", body: value, csrf: true, signal })).data as AuthCollection,
    updateAuthCollection: async (id: string, value: Pick<AuthCollection, "registrationEnabled" | "profileSchema">, signal?: AbortSignal): Promise<AuthCollection> =>
      (await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}`, method: "PATCH", body: value, csrf: true, signal })).data as AuthCollection,
    deleteAuthCollection: async (id: string, signal?: AbortSignal): Promise<void> => { await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}`, method: "DELETE", csrf: true, signal }); },
    getAdministration: async (id: string, signal?: AbortSignal): Promise<AuthAdministration> => (await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/administration`, signal })).data as AuthAdministration,
    setPolicy: async (id: string, value: AuthPolicy, signal?: AbortSignal): Promise<AuthPolicy> => (await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/policy`, method: "PUT", body: value, csrf: true, signal })).data as AuthPolicy,
    putProvider: async (id: string, value: OAuthProviderSettings, signal?: AbortSignal): Promise<OAuthProviderSettings> => (await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/oauth-providers/${encodeURIComponent(value.name)}`, method: "PUT", body: providerInput(value), csrf: true, signal })).data as OAuthProviderSettings,
    deleteProvider: async (id: string, name: string, signal?: AbortSignal): Promise<void> => { await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/oauth-providers/${encodeURIComponent(name)}`, method: "DELETE", csrf: true, signal }); },
    putTemplate: async (id: string, value: AuthMailTemplate, signal?: AbortSignal): Promise<AuthMailTemplate> => (await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/mail-templates/${value.purpose}`, method: "PUT", body: templateInput(value), csrf: true, signal })).data as AuthMailTemplate,
    previewTemplate: async (id: string, value: AuthMailTemplate & { actionUrl: string }, signal?: AbortSignal): Promise<AuthMailPreview> => (await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/mail-templates/${value.purpose}/preview`, method: "POST", body: { ...templateInput(value), actionUrl: value.actionUrl }, csrf: true, signal })).data as AuthMailPreview,
    putSMTP: async (id: string, value: AuthSMTPSettings, signal?: AbortSignal): Promise<AuthSMTPSettings> => (await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/smtp`, method: "PUT", body: smtpInput(value), csrf: true, signal })).data as AuthSMTPSettings,
    testSMTP: async (id: string, recipient: string, signal?: AbortSignal): Promise<void> => { await this.#schemaCall({ path: `/api/admin/auth-collections/${encodeURIComponent(id)}/smtp/test`, method: "POST", body: { recipient }, csrf: true, signal }); },
  };
  readonly serviceTokens = {
    listServiceTokens: async (signal?: AbortSignal): Promise<ServiceToken[]> =>
      ((await this.#schemaCall({ path: "/api/admin/service-tokens", signal })).data as { items: ServiceToken[] }).items,
    issueServiceToken: async (value: { name: string; ttlSeconds: number; scopes: ServiceTokenScope[] }, signal?: AbortSignal): Promise<IssuedServiceToken> =>
      (await this.#schemaCall({ path: "/api/admin/service-tokens", method: "POST", body: value, csrf: true, signal })).data as IssuedServiceToken,
    revokeServiceToken: async (id: string, signal?: AbortSignal): Promise<void> => {
      await this.#schemaCall({ path: `/api/admin/service-tokens/${encodeURIComponent(id)}/revoke`, method: "POST", csrf: true, signal });
    },
  };

  #fieldFilePath(collection: string, recordID: string, field: string): string { return `/api/admin/collections/${encodeURIComponent(collection)}/records/${encodeURIComponent(recordID)}/files/${encodeURIComponent(field)}`; }
  #filePath(id: string): string { return `/api/admin/files/${encodeURIComponent(id)}`; }
  async #fileCall(path: string, request: Omit<Request, "path">): Promise<any> {
    if (request.body instanceof FormData) {
      const response = await fetch(path, { method: request.method, headers: request.headers, body: request.body, credentials: "include", signal: request.signal });
      if (!response.ok) throw new RecordError(response.status === 404 ? "not-found" : "failed");
      return response.status === 204 ? undefined : decode(response);
    }
    return (await this.#recordCall({ ...request, path })).data;
  }

  #adopt(reply: Reply): AuthenticatedSession {
    const session = reply.data as AuthenticatedSession & { csrfToken: string };
    this.#csrfToken = session.csrfToken;
    this.#authGeneration += 1;
    return { administrator: session.administrator, expiresAt: session.expiresAt, sqlConsoleEnabled: session.sqlConsoleEnabled === true };
  }

  async #authenticate(path: string, body: unknown): Promise<AuthenticatedSession> {
    try {
      return this.#adopt(await this.#send({ path, method: "POST", body, anonymous: true }));
    } catch (caught: unknown) {
      throw caught instanceof WireError ? sessionFailure(caught.wire) : caught;
    }
  }

  async #schemaCall(request: Request): Promise<Reply> {
    try {
      return await this.#send(request);
    } catch (caught: unknown) {
      if (!(caught instanceof WireError)) throw caught;
      throw new SchemaError(this.#schemaReason(caught.wire), caught.wire.code, caught.wire.details);
    }
  }

  #schemaReason(wire: Wire): SchemaError["reason"] {
    if (wire.code === "collection_referenced") return "referenced";
    if (wire.code === "invalid_cursor") return "invalid-cursor";
    // One shape answers a tampered id, a foreign owner's operation and an id
    // that never existed, so the adapter cannot tell them apart either.
    if (wire.status === 404) return "not-found";
    if (wire.status === 422) return "data-blocked";
    return "failed";
  }

  // The system surface has no reasons a view acts on, but a transport type must
  // not escape the adapter either.
  #systemFailure(caught: unknown): unknown {
    return caught instanceof WireError ? new Error(caught.wire.code) : caught;
  }

  // A same-version 409 conflicts with what already exists; a moved catalog
  // means the approved plan is simply out of date. Only re-reading the catalog
  // separates them, and a deletion is never re-read: a referenced collection is
  // the one conflict it can report on its own.
  async #applyFailure(caught: unknown, plan: PlannedChange): Promise<unknown> {
    if (!(caught instanceof WireError)) return caught;
    const { status, code, details } = caught.wire;
    // The ledger's own verdicts are already decided, so they never take the
    // catalog re-read below: a rejected identity and a failed operation are
    // not a plan that went stale.
    if (code === "schema_operation_conflict") return new SchemaError("operation-conflict", code, details);
    if (code === "schema_operation_failed") return new SchemaError("operation-failed", code, details);
    if (plan.operation === "delete") {
      if (code === "collection_referenced") return new SchemaError("referenced", code, details);
      return new SchemaError(status === 409 ? "catalog-moved" : this.#schemaReason(caught.wire), code, details);
    }
    if (status !== 409) return new SchemaError(this.#schemaReason(caught.wire), code, details);
    try {
      const catalog = await this.schema.catalog();
      if (catalog.version === plan.baseVersion) return new SchemaError("same-version-conflict", code, details);
    } catch { /* an unreadable catalog cannot prove the plan is still current */ }
    return new SchemaError("catalog-moved", code, details);
  }

	async #recordCall(request: Request): Promise<Reply> {
    try {
      return await this.#send(request);
    } catch (caught: unknown) {
      if (!(caught instanceof WireError)) throw caught;
      throw recordFailure(caught.wire);
		}
	}

	async #multipartRecord(collection: string, id: string | undefined, input: RecordData, files: Record<string, File[]>, headers: Record<string, string>): Promise<VersionedRecord> {
		const body = new FormData();
		const recordBody: RecordData = { ...input };
		const parts: Array<{ name: string; file: File }> = [];
		let part = 0;
		for (const [field, selected] of Object.entries(files)) {
			const references = selected.map((file) => {
				const name = `file_${part++}`;
				parts.push({ name, file });
				return { $file: name };
			});
			if (references.length > 0) recordBody[field] = references.length === 1 ? references[0] : references;
		}
		const method = id === undefined ? "POST" : "PATCH";
		body.append("requests", stringifyJSON({ requests: [{ method, url: recordPath(collection, id), body: recordBody, headers }] }));
		for (const value of parts) body.append(value.name, value.file, value.file.name);
		const response = await fetch("/api/v1/collections/batch", { method: "POST", body, headers: { "X-PgBase-CSRF": this.#csrfToken }, credentials: "include" });
		const decoded = await decode(response).catch(() => undefined);
		if (!response.ok) {
			const reported = envelope(decoded);
			throw recordFailure({ status: response.status, code: reported?.code ?? "request_failed", details: reported?.details ?? {} });
		}
		const result = (decoded as { results?: Array<{ body?: RecordData; etag?: string }> })?.results?.[0];
		const record = result?.body;
		const etag = result?.etag;
		if (record === undefined || typeof etag !== "string") throw new RecordError("failed");
		return new VersionedRecord(collection, String(record.id ?? id ?? ""), record, etag);
	}

  #versioned(collection: string, id: string, reply: Reply): VersionedRecord {
    const etag = strongETag(reply.headers);
    if (etag === null) throw new RecordError("failed");
    return new VersionedRecord(collection, id, reply.data as RecordData, etag);
  }

  async #send(request: Request): Promise<Reply> {
    const authGeneration = this.#authGeneration;
    const headers = new Headers(request.headers);
    headers.set("Accept", "application/json");
    if (request.csrf === true) headers.set("X-PgBase-CSRF", this.#csrfToken);
    const body = request.body === undefined ? undefined : stringifyJSON(request.body);
    if (body !== undefined) headers.set("Content-Type", "application/json");

    const response = await fetch(request.path, { method: request.method, body, headers, credentials: "include", signal: request.signal });
    if (response.ok) {
      if (response.status === 204) return { data: undefined, headers: new Headers(response.headers), status: response.status };
      try {
        return { data: await decode(response), headers: new Headers(response.headers), status: response.status };
      } catch {
        // A 2xx whose body cannot be read is not a usable success: callers must
        // not treat the missing payload as an empty result.
        throw new WireError({ status: response.status, code: "invalid_response_body", details: {} });
      }
    }

    let reported: Omit<Wire, "status"> | null = null;
    try { reported = envelope(await decode(response)); } catch { /* a failure body is best effort */ }
    if (response.status === 401 && request.anonymous !== true && authGeneration === this.#authGeneration) for (const listener of [...this.#expired]) listener();
    throw new WireError({ status: response.status, code: reported?.code ?? "request_failed", details: reported?.details ?? {} });
  }

	readonly sqlConsoleTransport: SQLConsoleTransport = {
		request: async (request) => {
			const authGeneration = this.#authGeneration;
			const headers = new Headers(request.headers);
			headers.set("Accept", "application/json");
			if (request.method === "POST") headers.set("X-PgBase-CSRF", this.#csrfToken);
			const body = request.body === undefined ? undefined : stringifyJSON(request.body);
			if (body !== undefined) headers.set("Content-Type", "application/json");
			const response = await fetch(request.path, {
				method: request.method, body, headers, credentials: "include", signal: request.signal,
			});
			let decoded: unknown;
			try { decoded = await decode(response); } catch { decoded = undefined; }
			if (response.status === 401 && authGeneration === this.#authGeneration) {
				for (const listener of [...this.#expired]) listener();
			}
			return { status: response.status, body: decoded };
		},
	};
}

export function httpBackend(): HTTPBackend {
  return new HTTPBackend();
}
