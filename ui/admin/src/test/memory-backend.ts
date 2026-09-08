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
  type ApplyAttempt,
  type ApplyOutcome,
  type CreateAttempt,
  type ListOptions,
  type LoginInput,
  type OperationHistory,
  type OperationReceipt,
  type RecordData,
  type RecordPage,
  type ResumedSession,
  type SetupInput,
  type SchemaHealth,
	type SystemStatus,
	type ManagedUser, type UserPage,
	type FileInfo,
	type RecordRules,
	type RulePreview,
	type AuthCollection,
	type ServiceToken,
	type IssuedServiceToken,
} from "../backend/ports";
import { nextStep, settledStep, type BackendScript, type Fault, type ScriptedApply, type ScriptedCatalog, type ScriptedPlan, type ScriptedRecord } from "./backend-script";

type Surface = "session" | "schema" | "record" | "system";

const schemaCodes: Partial<Record<Fault["kind"], string>> = {
  "same-version-conflict": "schema_conflict",
  "catalog-moved": "schema_conflict",
  "data-blocked": "validation_failed",
  referenced: "collection_referenced",
  "operation-conflict": "schema_operation_conflict",
  "operation-failed": "schema_operation_failed",
  "invalid-cursor": "invalid_cursor",
  "not-found": "not_found",
  unauthorized: "authentication_required",
  unreachable: "internal_error",
};

const schemaReasons: Partial<Record<Fault["kind"], "operation-conflict" | "operation-failed" | "invalid-cursor" | "not-found">> = {
  "operation-conflict": "operation-conflict",
  "operation-failed": "operation-failed",
  "invalid-cursor": "invalid-cursor",
  "not-found": "not-found",
};

// A generic backend failure still has to reach the caller as that surface's own
// typed error; only the HTTP adapter is allowed to know it began as a status.
function failure(fault: Fault, surface: Surface): Error {
  // The ledger surface answers the same wire codes on the schema port that the
  // record port answers on its own, so the surface decides the typed error.
  const schemaReason = schemaReasons[fault.kind];
  if (surface === "schema" && schemaReason !== undefined) {
    return new SchemaError(schemaReason, schemaCodes[fault.kind] ?? "request_failed", ("details" in fault ? fault.details : undefined) ?? {});
  }
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

  async function take<T>(operation: string, detail: Record<string, unknown>, surface: Surface, anonymous = false): Promise<T> {
    const step = await settledStep(nextStep<T>(script, operation as never, detail));
    if ("fault" in step) {
      if (step.fault.kind === "unauthorized" && !anonymous) for (const listener of [...expired]) listener();
      throw failure(step.fault, surface);
    }
    return step.value;
  }

  async function guard<T>(signal: AbortSignal | undefined, produce: () => Promise<T>): Promise<T> {
    if (signal?.aborted === true) throw signal.reason ?? new Error("aborted");
    return produce();
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
      catalog: (signal) => guard(signal, async () => {
        const scripted = await take<ScriptedCatalog>("catalog", {}, "schema");
        return new CollectionCatalog(scripted.version, scripted.collections);
      }),
      plan: (change: CollectionChange, signal) => guard(signal, async () => {
        const scripted = await take<ScriptedPlan>("plan", { change }, "schema");
        return new PlannedChange(change, scripted.checksum, scripted.baseVersion, scripted.fields, scripted.sql);
      }),
      apply: (plan: PlannedChange, attempt: ApplyAttempt) => guard(undefined, async (): Promise<ApplyOutcome> => {
        const identity = attempt.identityFor(plan);
        const scripted = await take<ScriptedApply | undefined>("apply", { request: plan.applyRequest(), ...identity }, "schema");
        if (scripted === undefined) return { kind: "applied" };
        // A committed receipt is the ledger answering, so it carries no
        // collection; only a collection body does.
        if ("collection" in scripted) return { kind: "applied", definition: scripted.collection };
        if (scripted.status === "committed") return { kind: "applied" };
        return { kind: "undecided", operationId: identity.operationId };
      }),
      operation: (operationId: string, signal) => guard(signal, () => take<OperationReceipt>("operation", { operationId }, "schema")),
      operations: (cursor: string | undefined, signal) => guard(signal, () =>
        take<OperationHistory>("operations", cursor === undefined ? {} : { cursor }, "schema")),
		userAccess: (collection, signal) => guard(signal, () => take<{ collection: string; enabled: boolean }>("userAccess", { collection }, "schema")),
		setUserAccess: (collection, enabled) => guard(undefined, () => take<{ collection: string; enabled: boolean }>("setUserAccess", { collection, enabled }, "schema")),
    },
    records: {
      list: (collection: string, options: ListOptions, signal) => guard(signal, async () => {
        const detail: Record<string, unknown> = { collection };
        for (const key of ["cursor", "filter", "sort"] as const) if (options[key]) detail[key] = options[key];
        return take<RecordPage>("list", detail, "record");
      }),
      get: (collection: string, id: string, signal) =>
        guard(signal, async () => versioned(collection, id, await take<ScriptedRecord>("read", { collection, id }, "record"))),
      create: (attempt: CreateAttempt, input: RecordData) => guard(undefined, async () => {
        const scripted = await take<ScriptedRecord>("create", { collection: attempt.collection, input, key: attempt.keyFor(input) }, "record");
        return versioned(attempt.collection, String(scripted.data.id ?? ""), scripted);
      }),
      update: (record: VersionedRecord, patch: RecordData) => guard(undefined, async () => {
        const scripted = await take<ScriptedRecord>("update", { collection: record.collection, id: record.id, patch, etag: record.precondition() }, "record");
        return versioned(record.collection, record.id, scripted);
      }),
      remove: (record: VersionedRecord) =>
        guard(undefined, () => take<void>("remove", { collection: record.collection, id: record.id, etag: record.precondition() }, "record")),
    },
	system: {
      status: (signal) => guard(signal, () => take<SystemStatus>("status", {}, "system")),
      health: (signal) => guard(signal, () => take<SchemaHealth>("health", {}, "system")),
	},
	users: {
		list: (after, signal) => guard(signal, () => take<UserPage>("users", after ? { after } : {}, "system")),
		setActive: (id, active) => guard(undefined, () => take<ManagedUser>("setUserActive", { id, active }, "system")),
		revokeSessions: (id) => guard(undefined, () => take<number>("revokeUserSessions", { id }, "system")),
	},
	files: {
		listFiles: (collection, recordID, field, signal) => guard(signal, () => take<FileInfo[]>("listFiles", { collection, recordID, field }, "record")),
		uploadFile: (collection, recordID, field, file, signal) => guard(signal, () => take<FileInfo>("uploadFile", { collection, recordID, field, filename: file.name, size: file.size, type: file.type }, "record")),
		deleteFile: (id, signal) => guard(signal, () => take<void>("deleteFile", { id }, "record")),
		createDownloadToken: (id, variant, signal) => guard(signal, () => take<string>("createDownloadToken", { id, variant: variant ?? "original" }, "record")),
		downloadBlob: (id, token, variant, signal) => guard(signal, () => take<Blob>("downloadBlob", { id, token, variant: variant ?? "original" }, "record")),
	},
	rules: {
		getRules: (collection, signal) => guard(signal, () => take<RecordRules>("getRules", { collection }, "schema")),
		setRules: (collection, rules, revision, signal) => guard(signal, () => take<RecordRules>("setRules", { collection, rules, revision }, "schema")),
		previewRule: (collection, value, signal) => guard(signal, () => take<RulePreview>("previewRule", { collection, value }, "schema")),
	},
	authCollections: {
		listAuthCollections: (signal) => guard(signal, () => take<AuthCollection[]>("listAuthCollections", {}, "schema")),
		createAuthCollection: (value, signal) => guard(signal, () => take<AuthCollection>("createAuthCollection", { value }, "schema")),
		updateAuthCollection: (id, value, signal) => guard(signal, () => take<AuthCollection>("updateAuthCollection", { id, value }, "schema")),
		deleteAuthCollection: (id, signal) => guard(signal, () => take<void>("deleteAuthCollection", { id }, "schema")),
	},
	serviceTokens: {
		listServiceTokens: (signal) => guard(signal, () => take<ServiceToken[]>("listServiceTokens", {}, "schema")),
		issueServiceToken: (value, signal) => guard(signal, () => take<IssuedServiceToken>("issueServiceToken", { value }, "schema")),
		revokeServiceToken: (id, signal) => guard(signal, () => take<void>("revokeServiceToken", { id }, "schema")),
	},
  };
}
