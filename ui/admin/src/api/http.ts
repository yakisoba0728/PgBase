import { isSafeNumber, LosslessNumber, parse, stringify } from "lossless-json";

/** A failed API call. `code` is the server's `error.code`, or a transport/session failure. */
export class ApiError extends Error {
  status: number;
  code: string;
  details?: unknown;

  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

/** A 404: the thing asked for does not exist, or the server never registered the feature. */
export const isNotFound = (error: unknown) => error instanceof ApiError && error.status === 404;
export type Query = Record<string, string | number | boolean | undefined | null>;

export interface RequestOptions {
  query?: Query;
  body?: unknown;
  signal?: AbortSignal;
  headers?: Record<string, string>;
  /** Outlives the page (a sign-out followed at once by a reload or tab close). */
  keepalive?: boolean;
}

const csrfHeader = "X-PgBase-CSRF";
let csrfToken = "";
let generation = 0;
export const sessionGeneration = () => generation;
export const advanceSession = () => { generation++; };
export function setCsrfToken(token: string): void { csrfToken = token; }
export function clearCsrfToken(): void { csrfToken = ""; }

/** The administrator now signed in after renewing the session, or null when there is none. */
export type CsrfRecovery = () => Promise<string | null>;
let recover: CsrfRecovery = async () => null;
let identity: () => string | null = () => null;
interface RequestScope { generation: number; administrator: string | null }
interface RecoveryFlight { scope: RequestScope; promise: Promise<string | null> }
let recovering: RecoveryFlight | null = null;

/** Register session renewal and the identity under which a request is issued. */
export function onCsrfRejected(handler: CsrfRecovery, issuer: () => string | null): void {
  recover = handler;
  identity = issuer;
  recovering = null;
}

const sessionChanged = () => new ApiError(403, "session_changed", "The administrator session changed while the request was in flight");
const isCurrentScope = (scope: RequestScope) =>
  generation === scope.generation && (scope.administrator === null || identity() === scope.administrator);
function assertScope(scope: RequestScope): void {
  // An anonymous bootstrap read may discover the first identity. A request
  // issued as an administrator must never publish into another one's session.
  if (!isCurrentScope(scope)) throw sessionChanged();
}

async function inScope<T>(scope: RequestScope, attempt: () => Promise<T>): Promise<T> {
  assertScope(scope);
  try {
    const result = await attempt();
    assertScope(scope);
    return result;
  } catch (error) {
    // In particular, a delayed 401 must not invalidate a newer login.
    assertScope(scope);
    throw error;
  }
}

/** Keeps preparation, queued work and later requests in one administrator's session. */
export function captureSessionScope() {
  const scope: RequestScope = { generation, administrator: identity() };
  return {
    check: () => assertScope(scope),
    isCurrent: () => isCurrentScope(scope),
    run: <T>(attempt: () => Promise<T>) => inScope(scope, attempt),
  };
}

// The scope includes body decoding, not just arrival of the response headers.
// A CSRF rejection is safe to retry once, but only in the original session.
async function withCsrfRecovery<T>(attempt: () => Promise<T>): Promise<T> {
  const scope: RequestScope = { generation, administrator: identity() };
  try {
    return await inScope(scope, attempt);
  } catch (error) {
    if (!(error instanceof ApiError && error.code === "csrf_rejected")) throw error;
    assertScope(scope);
    if (!recovering || recovering.scope.generation !== scope.generation || recovering.scope.administrator !== scope.administrator) {
      const flight: RecoveryFlight = {
        scope,
        promise: Promise.resolve().then(recover).finally(() => {
          // An old recovery must not clear a newer session's single flight.
          if (recovering === flight) recovering = null;
        }),
      };
      recovering = flight;
    }
    const flight = recovering;
    const renewed = await inScope(scope, () => flight.promise);
    if (renewed === null) throw error;
    if (renewed !== scope.administrator) throw sessionChanged();
    return inScope(scope, attempt);
  }
}

// Only numbers that round-trip exactly become JS numbers; all others retain
// the server's exact decimal text through parsing and serialization.
const parseNumber = (value: string) => (isSafeNumber(value) ? Number(value) : new LosslessNumber(value));
export const parseJsonLossless = (text: string): unknown => parse(text, null, parseNumber);

// Every JSON number starts the text or follows `:`, `,` or `[` (plus whitespace), so this sees all of them, and
// sometimes digits inside a string as well, which only costs the fast path.
const numberToken = /(?:^|[:,[])\s*-?(\d+)(?:\.(\d+))?([eE]?)/g;

/**
 * False only when every number in the text is plain decimal with at most 15 digits: those convert to a double and
 * back to the same digits (DBL_DIG), so lossless-json would turn each into the same Number that JSON.parse gives.
 * An exponent, or more digits, may not round-trip and goes to the lossless parser.
 */
export function mayHoldUnsafeNumber(text: string): boolean {
  numberToken.lastIndex = 0;
  for (let match = numberToken.exec(text); match; match = numberToken.exec(text)) {
    if (match[3] || match[1].length + (match[2]?.length ?? 0) > 15) return true;
  }
  return false;
}

function countKeys(value: unknown): number {
  if (typeof value !== "object" || value === null) return 0;
  let count = 0;
  if (Array.isArray(value)) {
    for (const item of value) count += countKeys(item);
    return count;
  }
  for (const key in value) count += 1 + countKeys((value as Record<string, unknown>)[key]);
  return count;
}

const keyEnd = /"\s*:/g;
function countKeyEnds(text: string): number {
  keyEnd.lastIndex = 0;
  let count = 0;
  while (keyEnd.test(text)) count++;
  return count;
}

/**
 * JSON.parse when that gives exactly what the lossless parser would, which is much faster on list-sized bodies.
 * Besides numbers, the parsers differ on repeated keys (lossless-json rejects differing values, JSON.parse keeps the
 * last): every key ends in `"` and `:`, so as many key ends in the text as keys parsed means none repeated; strings
 * holding `":` only send more bodies to the lossless parser. Any text JSON.parse rejects is left to the lossless
 * parser too, so errors stay its own.
 */
export function parseJson(text: string): unknown {
  if (!mayHoldUnsafeNumber(text)) {
    let value: unknown;
    try {
      value = JSON.parse(text);
    } catch {
      return parseJsonLossless(text);
    }
    if (countKeys(value) === countKeyEnds(text)) return value;
  }
  return parseJsonLossless(text);
}
export function stringifyJson(value: unknown, space?: number): string {
  const text = stringify(value, null, space);
  if (text === undefined) throw new TypeError("value is not JSON-serializable");
  return text;
}

function buildURL(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null) params.set(key, String(value));
  }
  const encoded = params.toString();
  return encoded ? `${path}${path.includes("?") ? "&" : "?"}${encoded}` : path;
}

async function sendOnce(method: string, path: string, options: RequestOptions): Promise<Response> {
  const headers = new Headers(options.headers);
  let body: string | undefined;
  if (options.body !== undefined) {
    body = stringifyJson(options.body);
    if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  }
  if (method !== "GET" && csrfToken && !headers.has(csrfHeader)) headers.set(csrfHeader, csrfToken);
  let response: Response;
  try {
    response = await fetch(buildURL(path, options.query), {
      method, headers, body, credentials: "include", signal: options.signal, keepalive: options.keepalive,
    });
  } catch (error) {
    if (options.signal?.aborted) throw error;
    throw new ApiError(0, "network_error", error instanceof Error ? error.message : "Network request failed");
  }
  if (!response.ok) throw await toApiError(response);
  return response;
}

async function toApiError(response: Response): Promise<ApiError> {
  let envelope: unknown;
  try { envelope = parseJson(await response.text()); } catch { envelope = undefined; }
  const error = (envelope as { error?: { code?: unknown; message?: unknown; details?: unknown } } | undefined)?.error;
  if (error && typeof error.code === "string") {
    return new ApiError(response.status, error.code, typeof error.message === "string" ? error.message : error.code, error.details);
  }
  return new ApiError(response.status, "invalid_response", `Unexpected ${response.status} response`);
}

export function requestWithHeaders<T>(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  options: RequestOptions = {},
): Promise<{ data: T; headers: Headers }> {
  return withCsrfRecovery(async () => {
    const response = await sendOnce(method, path, options);
    const text = await response.text();
    if (text === "") return { data: undefined as T, headers: response.headers };
    try { return { data: parseJson(text) as T, headers: response.headers }; }
    catch { throw new ApiError(response.status, "invalid_response", "Response body is not valid JSON"); }
  });
}

export async function request<T>(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  return (await requestWithHeaders<T>(method, path, options)).data;
}

interface UploadOptions {
  headers?: Record<string, string>;
  /** Fraction of the body sent so far (0..1). */
  onProgress?: (fraction: number) => void;
  signal?: AbortSignal;
}

/** XHR supplies upload progress. Session boundaries and API errors match fetch. */
export function upload<T>(path: string, body: FormData | Blob, options: UploadOptions = {}): Promise<T> {
  return withCsrfRecovery(() => uploadOnce<T>(path, body, options));
}

function uploadOnce<T>(path: string, body: FormData | Blob, options: UploadOptions): Promise<T> {
  return new Promise((resolve, reject) => {
    const signal = options.signal;
    const abortReason = () => signal?.reason ?? new DOMException("Aborted", "AbortError");
    if (signal?.aborted) { reject(abortReason()); return; }
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    const cleanup = () => signal?.removeEventListener("abort", abort);
    const fail = (error: unknown) => { cleanup(); reject(error); };
    try {
      xhr.open("POST", path);
      xhr.withCredentials = true;
      const headers = new Headers(options.headers);
      for (const [key, value] of Object.entries(options.headers ?? {})) xhr.setRequestHeader(key, value);
      if (csrfToken && !headers.has(csrfHeader)) xhr.setRequestHeader(csrfHeader, csrfToken);
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable && event.total > 0) options.onProgress?.(Math.min(1, Math.max(0, event.loaded / event.total)));
      };
      xhr.onerror = () => fail(new ApiError(0, "network_error", "Network request failed"));
      xhr.ontimeout = () => fail(new ApiError(0, "network_error", "Upload timed out"));
      xhr.onabort = () => fail(abortReason());
      xhr.onload = () => {
        cleanup();
        if (xhr.status < 200 || xhr.status >= 300) {
          let response: Response;
          try { response = new Response(xhr.responseText, { status: xhr.status }); }
          catch { reject(new ApiError(xhr.status, "invalid_response", `Unexpected ${xhr.status} response`)); return; }
          void toApiError(response).then(reject, reject);
          return;
        }
        try { resolve((xhr.responseText === "" ? undefined : parseJson(xhr.responseText)) as T); }
        catch { reject(new ApiError(xhr.status, "invalid_response", "Response body is not valid JSON")); }
      };
      signal?.addEventListener("abort", abort, { once: true });
      // A custom transport may synchronously abort while it is being opened.
      if (signal?.aborted) { fail(abortReason()); return; }
      xhr.send(body);
    } catch (error) { fail(error); }
  });
}

export function requestBlob(path: string, options: RequestOptions = {}): Promise<Blob> {
  return withCsrfRecovery(async () => (await sendOnce("GET", path, options)).blob());
}
