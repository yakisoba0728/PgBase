import { parseJSON, stringifyJSON } from "./json";
import {
  CollectionCatalog,
  PlannedChange,
  RecordError,
  SchemaError,
  SessionError,
  VersionedRecord,
  type AdminBackend,
  type AuthenticatedSession,
  type CollectionChange,
  type CollectionDefinition,
  type CreateAttempt,
  type ListOptions,
  type PlannedField,
  type LoginInput,
  type RecordData,
  type RecordPage,
  type ResumedSession,
  type SetupInput,
  type SystemStatus,
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

function recordPath(collection: string, id?: string): string {
  const base = `/api/v1/collections/${encodeURIComponent(collection)}/records`;
  return id === undefined ? base : `${base}/${encodeURIComponent(id)}`;
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
  if (wire.code === "setup_unavailable") return new SessionError("setup-unavailable");
  if (wire.code === "validation_failed") return new SessionError("invalid-input");
  return new SessionError("failed");
}

class HTTPBackend implements AdminBackend {
  #csrfToken = "";
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
      try {
        await this.#send({
          path: "/api/admin/auth/logout",
          method: "POST",
          headers: { "X-CSRF-Token": this.#csrfToken, "X-PgBase-CSRF": this.#csrfToken },
        });
      } catch (caught: unknown) {
        throw caught instanceof WireError ? sessionFailure(caught.wire) : caught;
      }
      this.#csrfToken = "";
    },
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
      return new PlannedChange(change, plan.checksum, plan.baseVersion, plan.definition.fields, plan.sql);
    },
    apply: async (plan: PlannedChange): Promise<void> => {
      try {
        await this.#send({ path: "/api/admin/collections/apply", method: "POST", body: plan.applyRequest(), csrf: true });
      } catch (caught: unknown) {
        throw await this.#applyFailure(caught, plan);
      }
    },
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
  };

  #adopt(reply: Reply): AuthenticatedSession {
    const session = reply.data as AuthenticatedSession & { csrfToken: string };
    this.#csrfToken = session.csrfToken;
    return { administrator: session.administrator, expiresAt: session.expiresAt };
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

  #versioned(collection: string, id: string, reply: Reply): VersionedRecord {
    const etag = strongETag(reply.headers);
    if (etag === null) throw new RecordError("failed");
    return new VersionedRecord(collection, id, reply.data as RecordData, etag);
  }

  async #send(request: Request): Promise<Reply> {
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
    if (response.status === 401 && request.anonymous !== true) for (const listener of [...this.#expired]) listener();
    throw new WireError({ status: response.status, code: reported?.code ?? "request_failed", details: reported?.details ?? {} });
  }
}

export function httpBackend(): AdminBackend {
  return new HTTPBackend();
}
