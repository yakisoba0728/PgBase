import type {
  AuthenticatedSession,
  CollectionDefinition,
  PlannedField,
  RecordData,
  RecordPage,
  ResumedSession,
  SystemStatus,
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
  | { kind: "invalid-input" };

export type Step<T> = { value: T } | { fault: Fault };

export interface ScriptedCatalog { version: number; collections: CollectionDefinition[] }
export interface ScriptedPlan { baseVersion: number; checksum: string; fields: PlannedField[]; sql: string[] }
export interface ScriptedRecord { data: RecordData; etag: string }

export interface Call { operation: string; detail: Record<string, unknown> }

export interface BackendScript {
  resume: Step<ResumedSession>[];
  setup: Step<AuthenticatedSession>[];
  login: Step<AuthenticatedSession>[];
  logout: Step<void>[];
  catalog: Step<ScriptedCatalog>[];
  plan: Step<ScriptedPlan>[];
  apply: Step<void>[];
  list: Step<RecordPage>[];
  read: Step<ScriptedRecord>[];
  create: Step<ScriptedRecord>[];
  update: Step<ScriptedRecord>[];
  remove: Step<void>[];
  status: Step<SystemStatus>[];
  calls: Call[];
}

export function backendScript(): BackendScript {
  return {
    resume: [], setup: [], login: [], logout: [],
    catalog: [], plan: [], apply: [],
    list: [], read: [], create: [], update: [], remove: [],
    status: [], calls: [],
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
