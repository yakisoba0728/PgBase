import { vi } from "vitest";
import { stringifyJSON } from "../backend/json";
import { httpBackend } from "../backend/http-backend";
import type { AdminBackend, ResumedSession } from "../backend/ports";
import { nextStep, settledStep, type BackendScript, type Fault, type ScriptedApply, type ScriptedCatalog, type ScriptedPlan, type ScriptedRecord, type Step } from "./backend-script";
import type { OperationHistory, OperationReceipt } from "../backend/ports";

export interface Wire { status: number; body?: string; headers?: Record<string, string> }

export function wireResponse({ status, body, headers }: Wire): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "content-type": "application/json", ...headers }),
    text: vi.fn().mockResolvedValue(body ?? ""),
  } as unknown as Response;
}

export function errorBody(code: string, details: Record<string, string[]> = {}): string {
  return stringifyJSON({ error: { code, message: "scripted failure", details } });
}

// The wire shape each semantic fault takes on the real API. Conformance only
// holds if this translation and the scripted adapter reach the same typed error.
const faultWire: Record<Fault["kind"], (fault: Fault) => Wire> = {
  unauthorized: () => ({ status: 401, body: errorBody("authentication_required") }),
  unreachable: () => ({ status: 500, body: errorBody("internal_error") }),
  "not-found": () => ({ status: 404, body: errorBody("not_found") }),
  stale: () => ({ status: 412, body: errorBody("precondition_failed") }),
  rejected: (fault) => ({ status: 422, body: errorBody("validation_failed", details(fault)) }),
  "same-version-conflict": () => ({ status: 409, body: errorBody("schema_conflict") }),
  "catalog-moved": () => ({ status: 409, body: errorBody("schema_conflict") }),
  "data-blocked": (fault) => ({ status: 422, body: errorBody("validation_failed", details(fault)) }),
  referenced: (fault) => ({ status: 409, body: errorBody("collection_referenced", details(fault)) }),
  "invalid-credentials": () => ({ status: 401, body: errorBody("invalid_credentials") }),
  "setup-unavailable": () => ({ status: 409, body: errorBody("setup_unavailable") }),
  "invalid-input": () => ({ status: 400, body: errorBody("validation_failed") }),
  "operation-conflict": () => ({ status: 409, body: errorBody("schema_operation_conflict") }),
  "operation-failed": (fault) => ({ status: 409, body: errorBody("schema_operation_failed", details(fault)) }),
  "invalid-cursor": () => ({ status: 400, body: errorBody("invalid_cursor") }),
};

function details(fault: Fault): Record<string, string[]> {
  return ("details" in fault ? fault.details : undefined) ?? {};
}

async function fromStep<T>(pending: Step<T>, ok: (value: T) => Wire): Promise<Wire> {
  const step = await settledStep(pending);
  return "fault" in step ? faultWire[step.fault.kind](step.fault) : ok(step.value);
}

function catalogBody(scripted: ScriptedCatalog): string {
  return stringifyJSON({
    version: scripted.version,
    collections: Object.fromEntries(scripted.collections.map((collection) => [collection.name, collection])),
  });
}

function planBody(scripted: ScriptedPlan): string {
  return stringifyJSON({
    baseVersion: scripted.baseVersion,
    checksum: scripted.checksum,
    definition: { fields: scripted.fields },
    sql: scripted.sql,
  });
}

function recordWire(scripted: ScriptedRecord, status = 200): Wire {
  return { status, body: stringifyJSON(scripted.data), headers: { ETag: scripted.etag } };
}

/**
 * The four 2xx shapes a guarded apply can take on the real API: the collection
 * body, a replay, a committed outcome recovered after the request deadline, and
 * the 202 that says the outcome is not decidable yet.
 */
function applyWire(scripted: ScriptedApply | undefined, operationId: string): Wire {
  if (scripted === undefined) return { status: 200, body: "{}", headers: { "Idempotency-Replayed": "false" } };
  const statusUrl = `/api/admin/schema-operations/${operationId}`;
  const body = stringifyJSON({ operationId, status: scripted.status, statusUrl });
  if (scripted.replayed === true) return { status: 200, body, headers: { "Idempotency-Replayed": "true" } };
  if (scripted.status === "committed") return { status: 200, body };
  return { status: 202, body, headers: { Location: statusUrl, "Retry-After": "1" } };
}

/**
 * Replays one script over `fetch` so the production adapter runs its real
 * transport: paths, headers, statuses and body parsing all stay in play.
 */
export function httpHarness(script: BackendScript): { backend: AdminBackend; fetchMock: ReturnType<typeof vi.fn> } {
  let probe: "available" | "unavailable" | null = null;

  const fetchMock = vi.fn(async (path: string, init?: RequestInit): Promise<Response> => {
    const method = init?.method ?? "GET";
    if (init?.signal?.aborted === true) throw new DOMException("aborted", "AbortError");

    if (path === "/api/admin/session") {
      const step = await settledStep(nextStep<ResumedSession>(script, "resume"));
      if ("fault" in step) return wireResponse(faultWire[step.fault.kind](step.fault));
      const resumed = step.value;
      if (resumed.kind === "authenticated") return wireResponse({ status: 200, body: stringifyJSON({ ...resumed.session, csrfToken: "scripted-csrf" }) });
      if (resumed.kind === "setup-required") return wireResponse({ status: 404, body: errorBody("setup_required") });
      if (resumed.kind === "unreachable") return wireResponse({ status: 500, body: errorBody("internal_error") });
      probe = resumed.kind === "setup-unknown" ? "unavailable" : "available";
      return wireResponse({ status: 401, body: errorBody("authentication_required") });
    }
    if (path === "/api/admin/status") {
      const unavailable = probe === "unavailable";
      probe = null;
      return wireResponse(unavailable
        ? { status: 500, body: errorBody("internal_error") }
        : { status: 200, body: stringifyJSON({ setupRequired: false }) });
    }
    if (path === "/api/admin/setup" || path === "/api/admin/auth/login") {
      const operation = path === "/api/admin/setup" ? "setup" : "login";
      const step = nextStep<{ administrator: unknown; expiresAt: string }>(script, operation, { body: String(init?.body) });
      return wireResponse(await fromStep(step, (session) => ({ status: 200, body: stringifyJSON({ ...session, csrfToken: "scripted-csrf" }) })));
    }
    if (path === "/api/admin/auth/logout") {
      return wireResponse(await fromStep(nextStep<void>(script, "logout", { headers: init?.headers }), () => ({ status: 204 })));
    }
    if (path === "/api/admin/collections") {
      return wireResponse(await fromStep(nextStep<ScriptedCatalog>(script, "catalog"), (value) => ({ status: 200, body: catalogBody(value) })));
    }
    if (path === "/api/admin/collections/plan") {
      return wireResponse(await fromStep(nextStep<ScriptedPlan>(script, "plan", { body: String(init?.body) }), (value) => ({ status: 200, body: planBody(value) })));
    }
    if (path === "/api/admin/collections/apply") {
      const headers = new Headers(init?.headers);
      const operationId = headers.get("PgBase-Operation-ID") ?? "";
      const detail = { body: String(init?.body), operationId, idempotencyKey: headers.get("Idempotency-Key") ?? "" };
      const step = nextStep<ScriptedApply | undefined>(script, "apply", detail);
      return wireResponse(await fromStep(step, (value) => applyWire(value, operationId)));
    }
    const operations = /^\/api\/admin\/schema-operations(?:\/([^/?]+))?(?:\?(.*))?$/.exec(path);
    if (operations !== null) {
      if (operations[1] !== undefined) {
        const step = nextStep<OperationReceipt>(script, "operation", { operationId: decodeURIComponent(operations[1]) });
        return wireResponse(await fromStep(step, (value) => ({ status: 200, body: stringifyJSON(value) })));
      }
      const query = new URLSearchParams(operations[2] ?? "");
      const detail: Record<string, unknown> = { limit: query.get("limit") };
      if (query.get("cursor")) detail.cursor = query.get("cursor");
      return wireResponse(await fromStep(nextStep<OperationHistory>(script, "operations", detail), (value) => ({ status: 200, body: stringifyJSON(value) })));
    }
    if (path === "/api/admin/system/health") {
      return wireResponse(await fromStep(nextStep<unknown>(script, "health"), (value) => ({ status: 200, body: stringifyJSON(value) })));
    }
    if (path === "/api/admin/system") {
      return wireResponse(await fromStep(nextStep<unknown>(script, "status"), (value) => ({ status: 200, body: stringifyJSON(value) })));
    }

    const records = /^\/api\/v1\/collections\/([^/?]+)\/records(?:\/([^/?]+))?(?:\?(.*))?$/.exec(path);
    if (records === null) throw new Error(`unexpected request: ${method} ${path}`);
    const collection = decodeURIComponent(records[1]);
    const id = records[2] === undefined ? undefined : decodeURIComponent(records[2]);
    const query = new URLSearchParams(records[3] ?? "");
    if (method === "GET" && id === undefined) {
      const detail: Record<string, unknown> = { collection };
      for (const key of ["cursor", "filter", "sort"]) if (query.get(key)) detail[key] = query.get(key);
      return wireResponse(await fromStep(nextStep<unknown>(script, "list", detail), (value) => ({ status: 200, body: stringifyJSON(value) })));
    }
    if (method === "GET") {
      return wireResponse(await fromStep(nextStep<ScriptedRecord>(script, "read", { collection, id }), (value) => recordWire(value)));
    }
    if (method === "POST") {
      const step = nextStep<ScriptedRecord>(script, "create", { collection, body: String(init?.body), headers: init?.headers });
      return wireResponse(await fromStep(step, (value) => recordWire(value, 201)));
    }
    if (method === "PATCH") {
      const step = nextStep<ScriptedRecord>(script, "update", { collection, id, body: String(init?.body), headers: init?.headers });
      return wireResponse(await fromStep(step, (value) => recordWire(value)));
    }
    return wireResponse(await fromStep(nextStep<void>(script, "remove", { collection, id, headers: init?.headers }), () => ({ status: 204 })));
  });

  vi.stubGlobal("fetch", fetchMock);
  return { backend: httpBackend(), fetchMock };
}
