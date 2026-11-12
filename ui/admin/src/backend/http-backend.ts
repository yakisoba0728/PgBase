import {
  StorageMigrationNotFound,
  StorageMigrationRejected,
} from "./storage-port";
import { stringifyJSON } from "./json";
import type {
  FileStorageMigration,
  FileStorageMigrationInput,
  FileStorageSettings,
  FileStorageUpdate,
} from "./storage-port";
import type {
  BackupStorageSettings,
  BackupStorageUpdate,
} from "./backup-storage-port";
import { providerInput, smtpInput, templateInput } from "./auth-input";
import type { SQLConsoleTransport } from "./sql-console-backend";
import { createRecordsPort } from "./http-records-port";
import { createSessionPort } from "./http-session-port";
import { createSchemaPort } from "./http-schema-port";
import { callFile, createFilesPort } from "./http-files-port";
import { createAdminPorts } from "./http-admin-ports";
import { createAdministratorsPort } from "./http-administrators-port";
import {
  decode,
  envelope,
  type Reply,
  type Request,
  WireError,
  type Wire,
} from "./http-transport";
import {
  CollectionCatalog,
  PlannedChange,
  RecordError,
  SchemaError,
  SessionError,
  VersionedRecord,
  type AdminBackend,
  type RequestLogPage,
  type RequestLogEntry,
  type RequestLogFilter,
  type RequestLogStatistics,
  type JobPage,
  type BackupEntry,
  type BackupJob,
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
  type SetupBackup,
  type SetupBackupJob,
  type SystemStatus,
  type AdminSettings,
  type AdministratorProfile,
  type ManagedUser,
  type OAuthIdentity,
  type UserPage,
  type ImpersonatedUserSession,
  type FileInfo,
  type RecordRules,
  type RulePreview,
  type AuthCollection,
  type AuthAdministration,
  type AuthPolicy,
  type OAuthProviderSettings,
  type AuthMailTemplate,
  type AuthMailPreview,
  type AuthSMTPSettings,
  type IssuedServiceToken,
  type ServiceToken,
  type ServiceTokenScope,
  type SchemaDocument,
  type CollectionScaffolds,
  type SchemaImportPlan,
  type SchemaImportResult,
} from "./ports";

// A strong ETag is the only safe precondition for the next write, so a record
// response without one is a failed read rather than a record with no version.
function strongETag(headers: Headers): string | null {
  const etag = headers.get("ETag")?.trim();
  if (
    etag === undefined ||
    etag.length < 2 ||
    etag[0] !== '"' ||
    etag[etag.length - 1] !== '"'
  )
    return null;
  for (let index = 1; index < etag.length - 1; index += 1) {
    const code = etag.charCodeAt(index);
    if (
      code !== 0x21 &&
      !(code >= 0x23 && code <= 0x7e) &&
      !(code >= 0x80 && code <= 0xff)
    )
      return null;
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
  if (wire.committedVersion !== undefined)
    bounded.committedVersion = Number(wire.committedVersion);
  if (wire.failureCode !== undefined)
    bounded.failureCode = String(wire.failureCode);
  if (wire.completedAt !== undefined)
    bounded.completedAt = String(wire.completedAt);
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
    return Array.isArray(body?.fields)
      ? { kind: "applied", definition: data as CollectionDefinition }
      : { kind: "applied" };
  }
  return body.status === "committed"
    ? { kind: "applied" }
    : { kind: "undecided", operationId };
}

function recordPath(collection: string, id?: string): string {
  const base = `/api/v1/collections/${encodeURIComponent(collection)}/records`;
  return id === undefined ? base : `${base}/${encodeURIComponent(id)}`;
}

function fileIdentity(files: Record<string, File[]>): Record<string, string[]> {
  return Object.fromEntries(
    Object.entries(files).map(([field, selected]) => [
      field,
      selected.map(
        (file) => `${file.name}:${file.size}:${file.type}:${file.lastModified}`,
      ),
    ]),
  );
}

function definitions(raw: unknown): CollectionDefinition[] {
  const collections =
    (raw as { collections?: Record<string, CollectionDefinition> })
      .collections ?? {};
  return Object.values(collections);
}

function recordFailure(wire: Wire): RecordError {
  if (wire.status === 404) return new RecordError("not-found");
  if (wire.status === 412) return new RecordError("stale");
  if (wire.code === "invalid_record_id" || wire.code === "record_id_conflict")
    return new RecordError("rejected", { id: [wire.code] });
  if (wire.code === "validation_failed")
    return new RecordError("rejected", wire.details);
  return new RecordError("failed");
}

function sessionFailure(wire: Wire): SessionError {
  if (wire.code === "invalid_credentials")
    return new SessionError("invalid-credentials");
  if (wire.code === "otp_required") return new SessionError("otp-required");
  if (wire.code === "setup_unavailable")
    return new SessionError("setup-unavailable");
  if (wire.code === "validation_failed")
    return new SessionError("invalid-input");
  return new SessionError("failed");
}

class HTTPBackend implements AdminBackend {
  #csrfToken = "";
  #authGeneration = 0;
  #administratorID = "";
  readonly #expired = new Set<() => void>();

  /*
   * Session endpoint codecs live beside the other port adapters.  This class
   * keeps the one mutable credential boundary so every port observes the same
   * CSRF token and authentication generation.
   */
  readonly session = createSessionPort({
    send: (request) => this.#send(request),
    adopt: (reply) => this.#adopt(reply),
    csrfToken: () => this.#csrfToken,
    authGeneration: () => this.#authGeneration,
    clearSession: (generation) => {
      if (generation === this.#authGeneration) {
        this.#csrfToken = "";
        this.#administratorID = "";
        this.#authGeneration += 1;
      }
    },
    onExpired: (listener) => {
      this.#expired.add(listener);
      return () => this.#expired.delete(listener);
    },
  });

  readonly schema = createSchemaPort({
    send: (request) => this.#send(request),
    schemaCall: (request) => this.#schemaCall(request),
    applyFailure: (caught, plan) => this.#applyFailure(caught, plan),
  });


  readonly records = createRecordsPort({
    recordCall: (request) => this.#recordCall(request),
    versioned: (collection, id, reply) =>
      this.#versioned(collection, id, reply),
    multipartRecord: (collection, id, input, files, headers) =>
      this.#multipartRecord(collection, id, input, files, headers),
  });
  readonly files = createFilesPort({
    recordCall: (request) => this.#recordCall(request),
    fileCall: (path, request) =>
      callFile(
        (value) => this.#recordCall(value),
        (input, init) => this.#protectedFetch(input, init),
        path,
        request,
      ),
    csrfToken: () => this.#csrfToken,
    protectedFetch: (input, init) => this.#protectedFetch(input, init),
  });

  readonly adminPorts = createAdminPorts({
    send: (request) => this.#send(request),
    schemaCall: (request) => this.#schemaCall(request),
    systemFailure: (caught) => this.#systemFailure(caught),
    csrfToken: () => this.#csrfToken,
    protectedFetch: (input, init) => this.#protectedFetch(input, init),
  });

  readonly administrators = createAdministratorsPort({
    send: (request) => this.#send(request),
    authGeneration: () => this.#authGeneration,
    sessionsRevoked: (id, generation) => {
      if (generation !== this.#authGeneration || id !== this.#administratorID) return;
      this.#csrfToken = "";
      this.#administratorID = "";
      this.#authGeneration += 1;
      for (const listener of [...this.#expired]) listener();
    },
  });

  get capabilities() { return this.#capabilities; }
  get realtime() { return this.#capabilities?.realtime === false ? undefined : this.adminPorts.realtime; }
  #capabilities: import("./ports").AdminCapabilities | undefined;

  get system() { return this.adminPorts.system; }
  get settings() { return this.#capabilities?.settings === false ? undefined : this.adminPorts.settings; }
  get profile() { return this.#capabilities?.profile === false ? undefined : this.adminPorts.profile; }
  get operations() { return this.adminPorts.operations; }
  get storage() { return this.adminPorts.storage; }
  get backupStorage() { return this.adminPorts.backupStorage; }
  get users() { return this.adminPorts.users; }
  get rules() { return this.adminPorts.rules; }
  get authCollections() { return this.adminPorts.authCollections; }
  get serviceTokens() { return this.adminPorts.serviceTokens; }

  #adopt(reply: Reply): AuthenticatedSession {
    const session = reply.data as AuthenticatedSession & { csrfToken: string };
    this.#capabilities = session.capabilities;
    this.#csrfToken = session.csrfToken;
    this.#administratorID = session.administrator.id;
    this.#authGeneration += 1;
    return {
      administrator: session.administrator,
      expiresAt: session.expiresAt,
      sqlConsoleEnabled: session.sqlConsoleEnabled === true,
      ...(session.capabilities === undefined ? {} : { capabilities: session.capabilities }),
    };
  }

  async #authenticate(
    path: string,
    body: unknown,
  ): Promise<AuthenticatedSession> {
    try {
      return this.#adopt(
        await this.#send({ path, method: "POST", body, anonymous: true }),
      );
    } catch (caught: unknown) {
      throw caught instanceof WireError ? sessionFailure(caught.wire) : caught;
    }
  }

  async #schemaCall(request: Request): Promise<Reply> {
    try {
      return await this.#send(request);
    } catch (caught: unknown) {
      if (!(caught instanceof WireError)) throw caught;
      throw new SchemaError(
        this.#schemaReason(caught.wire),
        caught.wire.code,
        caught.wire.details,
      );
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
    if (code === "schema_operation_conflict")
      return new SchemaError("operation-conflict", code, details);
    if (code === "schema_operation_failed")
      return new SchemaError("operation-failed", code, details);
    if (plan.operation === "delete") {
      if (code === "collection_referenced")
        return new SchemaError("referenced", code, details);
      return new SchemaError(
        status === 409 ? "catalog-moved" : this.#schemaReason(caught.wire),
        code,
        details,
      );
    }
    if (status !== 409)
      return new SchemaError(this.#schemaReason(caught.wire), code, details);
    try {
      const catalog = await this.schema.catalog();
      if (catalog.version === plan.baseVersion)
        return new SchemaError("same-version-conflict", code, details);
    } catch {
      /* an unreadable catalog cannot prove the plan is still current */
    }
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

  async #multipartRecord(
    collection: string,
    id: string | undefined,
    input: RecordData,
    files: Record<string, File[]>,
    headers: Record<string, string>,
  ): Promise<VersionedRecord> {
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
      if (references.length > 0)
        recordBody[field] =
          references.length === 1 ? references[0] : references;
    }
    const method = id === undefined ? "POST" : "PATCH";
    body.append(
      "requests",
      stringifyJSON({
        requests: [
          {
            method,
            url: recordPath(collection, id),
            body: recordBody,
            headers,
          },
        ],
      }),
    );
    for (const value of parts)
      body.append(value.name, value.file, value.file.name);
    const response = await this.#protectedFetch("/api/v1/collections/batch", {
      method: "POST",
      body,
      headers: { "X-PgBase-CSRF": this.#csrfToken },
      credentials: "include",
    });
    const decoded = await decode(response).catch(() => undefined);
    if (!response.ok) {
      const reported = envelope(decoded);
      throw recordFailure({
        status: response.status,
        code: reported?.code ?? "request_failed",
        details: reported?.details ?? {},
      });
    }
    const result = (
      decoded as { results?: Array<{ body?: RecordData; etag?: string }> }
    )?.results?.[0];
    const record = result?.body;
    const etag = result?.etag;
    if (record === undefined || typeof etag !== "string")
      throw new RecordError("failed");
    return new VersionedRecord(
      collection,
      String(record.id ?? id ?? ""),
      record,
      etag,
    );
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
    const body =
      request.body === undefined ? undefined : stringifyJSON(request.body);
    if (body !== undefined) headers.set("Content-Type", "application/json");

    const response = await fetch(request.path, {
      method: request.method,
      body,
      headers,
      credentials: "include",
      signal: request.signal,
    });
    if (response.ok) {
      if (response.status === 204)
        return {
          data: undefined,
          headers: new Headers(response.headers),
          status: response.status,
        };
      try {
        return {
          data: await decode(response),
          headers: new Headers(response.headers),
          status: response.status,
        };
      } catch {
        // A 2xx whose body cannot be read is not a usable success: callers must
        // not treat the missing payload as an empty result.
        throw new WireError({
          status: response.status,
          code: "invalid_response_body",
          details: {},
        });
      }
    }

    let reported: Omit<Wire, "status"> | null = null;
    try {
      reported = envelope(await decode(response));
    } catch {
      /* a failure body is best effort */
    }
    if (
      response.status === 401 &&
      request.anonymous !== true &&
      authGeneration === this.#authGeneration
    )
      for (const listener of [...this.#expired]) listener();
    throw new WireError({
      status: response.status,
      code: reported?.code ?? "request_failed",
      details: reported?.details ?? {},
    });
  }

  async #protectedFetch(
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> {
    const authGeneration = this.#authGeneration;
    const response = await fetch(input, init);
    if (response.status === 401 && authGeneration === this.#authGeneration) {
      for (const listener of [...this.#expired]) listener();
    }
    return response;
  }

  readonly sqlConsoleTransport: SQLConsoleTransport = {
    request: async (request) => {
      const authGeneration = this.#authGeneration;
      const headers = new Headers(request.headers);
      headers.set("Accept", "application/json");
      if (request.method === "POST")
        headers.set("X-PgBase-CSRF", this.#csrfToken);
      const body =
        request.body === undefined ? undefined : stringifyJSON(request.body);
      if (body !== undefined) headers.set("Content-Type", "application/json");
      const response = await fetch(request.path, {
        method: request.method,
        body,
        headers,
        credentials: "include",
        signal: request.signal,
      });
      let decoded: unknown;
      try {
        decoded = await decode(response);
      } catch {
        decoded = undefined;
      }
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
