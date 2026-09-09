import type {
  AuthenticatedSession,
  CollectionDefinition,
  OperationHistory,
  OperationReceipt,
  OperationStatus,
  PlannedField,
  RecordData,
  RecordPage,
  ResumedSession,
  SchemaHealth,
	SystemStatus,
	ManagedUser,
	UserPage,
	FileInfo,
	RecordRules,
	RulePreview,
	AuthCollection,
	ServiceToken,
	IssuedServiceToken,
} from "../backend/ports";

/**
 * A semantic failure both adapters can realise: the HTTP harness turns it into
 * a wire response, the scripted adapter throws the typed error directly. A
 * conformance vector passes only when both routes reach the same typed error.
 */
export type Fault =
  | { kind: "unauthorized" }
  | { kind: "unreachable" }
  | { kind: "not-found" }
  | { kind: "stale" }
  | { kind: "rejected"; details?: Record<string, string[]> }
  | { kind: "same-version-conflict" }
  | { kind: "catalog-moved" }
  | { kind: "data-blocked"; details?: Record<string, string[]> }
  | { kind: "referenced"; details?: Record<string, string[]> }
  | { kind: "invalid-credentials" }
  | { kind: "setup-unavailable" }
  | { kind: "invalid-input" }
  | { kind: "operation-conflict" }
  | { kind: "operation-failed"; details?: Record<string, string[]> }
  | { kind: "invalid-cursor" };

export type Step<T> = { value: T } | { fault: Fault } | { pending: Promise<Step<T>> };

/** A step that stays in flight until the test settles it, so a late result can
 * be delivered after the caller has already moved to another resource. */
export function deferredStep<T>(): { step: Step<T>; settle(next: Step<T>): void } {
  let resolve: (next: Step<T>) => void = () => {};
  const pending = new Promise<Step<T>>((accept) => { resolve = accept; });
  return { step: { pending }, settle: (next) => resolve(next) };
}

export async function settledStep<T>(step: Step<T>): Promise<{ value: T } | { fault: Fault }> {
  let current = step;
  while ("pending" in current) current = await current.pending;
  return current;
}

export interface ScriptedCatalog { version: number; collections: CollectionDefinition[] }
export interface ScriptedPlan { baseVersion: number; checksum: string; fields: PlannedField[]; sql: string[] }
export interface ScriptedRecord { data: RecordData; etag: string }

/**
 * What an apply answers with: the collection the change produced, or the ledger
 * outcome it sends instead. A scripted `undefined` is an ordinary apply whose
 * body the vector does not inspect, so all three answers stay distinguishable.
 */
export type ScriptedApply =
  | { status: OperationStatus; replayed?: boolean }
  | { collection: CollectionDefinition };

/**
 * A fully sampled system. A test that cares about one component patches that
 * one, so the state it is not asserting on cannot drift into the assertion.
 */
export function scriptedStatus(patch: Partial<SystemStatus> = {}): SystemStatus {
  return {
    version: "v1.2.3", databaseReachable: true, migrationCurrent: 4, migrationLatest: 5,
    migrationDirty: true, catalogVersion: 9, listenerReady: false,
    unavailable: [], sampledAt: "2026-09-01T00:05:00Z", lastSuccessAt: "2026-09-01T00:05:00Z", ...patch,
  };
}

/** A completed diagnosis that found nothing, patched the same way. */
export function scriptedHealth(patch: Partial<SchemaHealth> = {}): SchemaHealth {
  return {
    state: "healthy", findings: [], findingCount: 0, unavailable: [],
    sampledAt: "2026-09-01T00:05:00Z", lastSuccessAt: "2026-09-01T00:05:00Z", retries: 0, ...patch,
  };
}

export interface Call { operation: string; detail: Record<string, unknown> }

export interface BackendScript {
  resume: Step<ResumedSession>[];
  setup: Step<AuthenticatedSession>[];
  login: Step<AuthenticatedSession>[];
  logout: Step<void>[];
  catalog: Step<ScriptedCatalog>[];
  plan: Step<ScriptedPlan>[];
  apply: Step<ScriptedApply | undefined>[];
  operation: Step<OperationReceipt>[];
  operations: Step<OperationHistory>[];
  list: Step<RecordPage>[];
  read: Step<ScriptedRecord>[];
	create: Step<ScriptedRecord>[];
	createWithFiles: Step<ScriptedRecord>[];
	update: Step<ScriptedRecord>[];
	updateWithFiles: Step<ScriptedRecord>[];
  remove: Step<void>[];
  status: Step<SystemStatus>[];
	health: Step<SchemaHealth>[];
	userAccess: Step<{ collection: string; enabled: boolean }>[];
	setUserAccess: Step<{ collection: string; enabled: boolean }>[];
	users: Step<UserPage>[];
	setUserActive: Step<ManagedUser>[];
	revokeUserSessions: Step<number>[];
	listFiles: Step<FileInfo[]>[];
	uploadFile: Step<FileInfo>[];
	deleteFile: Step<void>[];
	createDownloadToken: Step<string>[];
	downloadBlob: Step<Blob>[];
	getRules: Step<RecordRules>[];
	setRules: Step<RecordRules>[];
	previewRule: Step<RulePreview>[];
	listAuthCollections: Step<AuthCollection[]>[];
	createAuthCollection: Step<AuthCollection>[];
	updateAuthCollection: Step<AuthCollection>[];
	deleteAuthCollection: Step<void>[];
	listServiceTokens: Step<ServiceToken[]>[];
	issueServiceToken: Step<IssuedServiceToken>[];
	revokeServiceToken: Step<void>[];
  calls: Call[];
}

export function backendScript(): BackendScript {
  return {
    resume: [], setup: [], login: [], logout: [],
    catalog: [], plan: [], apply: [], operation: [], operations: [],
	list: [], read: [], create: [], createWithFiles: [], update: [], updateWithFiles: [], remove: [],
	status: [], health: [], calls: [],
	users: [], setUserActive: [], revokeUserSessions: [],
	listFiles: [], uploadFile: [], deleteFile: [], createDownloadToken: [], downloadBlob: [],
	getRules: [], setRules: [],
	previewRule: [],
	listAuthCollections: [], createAuthCollection: [], updateAuthCollection: [], deleteAuthCollection: [],
	listServiceTokens: [], issueServiceToken: [], revokeServiceToken: [],
	userAccess: [], setUserAccess: [],
  };
}

export function nextStep<T>(script: BackendScript, operation: keyof BackendScript & string, detail: Record<string, unknown> = {}): Step<T> {
  script.calls.push({ operation, detail });
  const queue = script[operation] as unknown as Step<T>[];
  const step = queue.shift();
  if (step === undefined) throw new Error(`unscripted backend call: ${operation}`);
  return step;
}

export function callsTo(script: BackendScript, operation: string): Call[] {
  return script.calls.filter((call) => call.operation === operation);
}
