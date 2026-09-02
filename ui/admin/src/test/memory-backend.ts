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
  type CreateAttempt,
  type ListOptions,
  type LoginInput,
  type RecordData,
  type RecordPage,
  type ResumedSession,
  type SetupInput,
  type SystemStatus,
} from "../backend/ports";
import { nextStep, type BackendScript, type Fault, type ScriptedCatalog, type ScriptedPlan, type ScriptedRecord, type Step } from "./backend-script";

type Surface = "session" | "schema" | "record" | "system";

const schemaCodes: Partial<Record<Fault["kind"], string>> = {
  "same-version-conflict": "schema_conflict",
  "catalog-moved": "schema_conflict",
  "data-blocked": "validation_failed",
  referenced: "collection_referenced",
  unauthorized: "authentication_required",
  unreachable: "internal_error",
};

// A generic backend failure still has to reach the caller as that surface's own
// typed error; only the HTTP adapter is allowed to know it began as a status.
function failure(fault: Fault, surface: Surface): Error {
  if (fault.kind === "not-found") return new RecordError("not-found");
  if (fault.kind === "stale") return new RecordError("stale");
  if (fault.kind === "rejected") return new RecordError("rejected", fault.details ?? {});
  if (fault.kind === "invalid-credentials") return new SessionError("invalid-credentials");
  if (fault.kind === "setup-unavailable") return new SessionError("setup-unavailable");
  if (fault.kind === "invalid-input") return new SessionError("invalid-input");
  if (fault.kind === "same-version-conflict" || fault.kind === "catalog-moved" || fault.kind === "referenced" || fault.kind === "data-blocked") {
    return new SchemaError(fault.kind, schemaCodes[fault.kind] ?? "request_failed", ("details" in fault ? fault.details : undefined) ?? {});
  }
  if (surface === "schema") return new SchemaError("failed", schemaCodes[fault.kind] ?? "request_failed");
  if (surface === "record") return new RecordError("failed");
  if (surface === "session") return new SessionError("failed");
  return new Error(fault.kind);
}

/**
 * Scripts workflow outcomes only. It never reimplements SQL, filtering, the
 * schema engine or HTTP status handling, so a vector that passes here and
 * against the HTTP adapter pins semantics rather than either transport.
 */
export function memoryBackend(script: BackendScript): AdminBackend {
  const expired = new Set<() => void>();

  function take<T>(operation: string, detail: Record<string, unknown>, surface: Surface, anonymous = false): T {
    const step = nextStep<T>(script, operation as never, detail) as Step<T>;
    if ("fault" in step) {
      if (step.fault.kind === "unauthorized" && !anonymous) for (const listener of [...expired]) listener();
      throw failure(step.fault, surface);
    }
    return step.value;
  }

  function guard<T>(signal: AbortSignal | undefined, produce: () => T): Promise<T> {
    if (signal?.aborted === true) return Promise.reject(signal.reason ?? new Error("aborted"));
    try { return Promise.resolve(produce()); } catch (caught: unknown) { return Promise.reject(caught); }
  }

  const versioned = (collection: string, id: string, scripted: ScriptedRecord) =>
    new VersionedRecord(collection, id, scripted.data, scripted.etag);

  return {
    session: {
      resume: (signal) => guard(signal, () => take<ResumedSession>("resume", {}, "session", true)),
      setup: (input: SetupInput) => guard(undefined, () => take<AuthenticatedSession>("setup", { ...input }, "session", true)),
      login: (input: LoginInput) => guard(undefined, () => take<AuthenticatedSession>("login", { ...input }, "session", true)),
      logout: () => guard(undefined, () => take<void>("logout", {}, "session")),
      onExpired: (listener) => {
        expired.add(listener);
        return () => { expired.delete(listener); };
      },
    },
    schema: {
      catalog: (signal) => guard(signal, () => {
        const scripted = take<ScriptedCatalog>("catalog", {}, "schema");
        return new CollectionCatalog(scripted.version, scripted.collections);
      }),
      plan: (change: CollectionChange, signal) => guard(signal, () => {
        const scripted = take<ScriptedPlan>("plan", { change }, "schema");
        return new PlannedChange(change, scripted.checksum, scripted.baseVersion, scripted.fields, scripted.sql);
      }),
      apply: (plan: PlannedChange) => guard(undefined, () => take<void>("apply", { request: plan.applyRequest() }, "schema")),
    },
    records: {
      list: (collection: string, options: ListOptions, signal) => guard(signal, () => {
        const detail: Record<string, unknown> = { collection };
        for (const key of ["cursor", "filter", "sort"] as const) if (options[key]) detail[key] = options[key];
        return take<RecordPage>("list", detail, "record");
      }),
      get: (collection: string, id: string, signal) =>
        guard(signal, () => versioned(collection, id, take<ScriptedRecord>("read", { collection, id }, "record"))),
      create: (attempt: CreateAttempt, input: RecordData) => guard(undefined, () => {
        const scripted = take<ScriptedRecord>("create", { collection: attempt.collection, input, key: attempt.keyFor(input) }, "record");
        return versioned(attempt.collection, String(scripted.data.id ?? ""), scripted);
      }),
      update: (record: VersionedRecord, patch: RecordData) => guard(undefined, () => {
        const scripted = take<ScriptedRecord>("update", { collection: record.collection, id: record.id, patch, etag: record.precondition() }, "record");
        return versioned(record.collection, record.id, scripted);
      }),
      remove: (record: VersionedRecord) =>
        guard(undefined, () => take<void>("remove", { collection: record.collection, id: record.id, etag: record.precondition() }, "record")),
    },
    system: {
      status: (signal) => guard(signal, () => take<SystemStatus>("status", {}, "system")),
    },
  };
}
