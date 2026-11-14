import { isSafeNumber, LosslessNumber, parse, stringify } from "lossless-json";

/** A failed API call. `code` is the server's `error.code`, or `network_error` /
 * `invalid_response` when the transport itself failed. */
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

/** A 404: the thing asked for does not exist, or (on a list route) the server never registered the feature. */
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

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

export function clearCsrfToken(): void {
  csrfToken = "";
}

/** The administrator now signed in after renewing the session, or null when there is none. */
export type CsrfRecovery = () => Promise<string | null>;
let recover: CsrfRecovery = async () => null;
let identity: () => string | null = () => null;
let recovering: Promise<string | null> | null = null;

/**
 * Registers how a rejected CSRF token is renewed (another tab signed in again and replaced the session cookie) and
 * which administrator a request is issued under.
 */
export function onCsrfRejected(handler: CsrfRecovery, issuer: () => string | null): void {
  recover = handler;
  identity = issuer;
}

// A rejected token never reached the handler, so the request is safe to send once more with the renewed one, but
// only for the administrator it was issued under: one recovery is shared, and the cache may already show someone else.
async function withCsrfRecovery<T>(attempt: () => Promise<T>): Promise<T> {
  const issuedBy = identity();
  try {
    return await attempt();
  } catch (error) {
    if (!(error instanceof ApiError && error.code === "csrf_rejected")) throw error;
    const renewed = await (recovering ??= recover().finally(() => (recovering = null)));
    if (renewed === null) throw error;
    if (renewed === issuedBy) return attempt();
    throw new ApiError(403, "session_changed", "Another administrator signed in on this browser");
  }
}

// A number becomes a JS number only when it round-trips exactly (safe integers
// and floats whose digits survive parseFloat); anything else (big int64s,
// over-long decimals, out-of-range exponents) stays a LosslessNumber holding
// the server's exact text, and stringifyJson writes it back unchanged.
export function parseJson(text: string): unknown {
  return parse(text, null, (value) => (isSafeNumber(value) ? Number(value) : new LosslessNumber(value)));
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

const send = (method: string, path: string, options: RequestOptions) => withCsrfRecovery(() => sendOnce(method, path, options));

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
      method,
      headers,
      body,
      credentials: "include",
      signal: options.signal,
      keepalive: options.keepalive,
    });
  } catch (error) {
    // An abort is the caller's own decision, so it propagates untouched.
    if (options.signal?.aborted) throw error;
    throw new ApiError(0, "network_error", error instanceof Error ? error.message : "Network request failed");
  }
  if (!response.ok) throw await toApiError(response);
  return response;
}

async function toApiError(response: Response): Promise<ApiError> {
  let envelope: unknown;
  try {
    envelope = parseJson(await response.text());
  } catch {
    envelope = undefined;
  }
  const error = (envelope as { error?: { code?: unknown; message?: unknown; details?: unknown } } | undefined)?.error;
  if (error && typeof error.code === "string") {
    const message = typeof error.message === "string" ? error.message : error.code;
    return new ApiError(response.status, error.code, message, error.details);
  }
  return new ApiError(response.status, "invalid_response", `Unexpected ${response.status} response`);
}

export async function requestWithHeaders<T>(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  options: RequestOptions = {},
): Promise<{ data: T; headers: Headers }> {
  const response = await send(method, path, options);
  const text = await response.text();
  if (text === "") return { data: undefined as T, headers: response.headers };
  try {
    return { data: parseJson(text) as T, headers: response.headers };
  } catch {
    throw new ApiError(response.status, "invalid_response", "Response body is not valid JSON");
  }
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

/** A POST over XHR, the one transport that reports upload progress; the body is a multipart form or a raw blob. Errors match `request`. */
export function upload<T>(path: string, body: FormData | Blob, options: UploadOptions = {}): Promise<T> {
  return withCsrfRecovery(() => uploadOnce<T>(path, body, options));
}

function uploadOnce<T>(path: string, body: FormData | Blob, options: UploadOptions): Promise<T> {
  return new Promise((resolve, reject) => {
    if (options.signal?.aborted) { reject(options.signal.reason ?? new DOMException("Aborted", "AbortError")); return; }
    const xhr = new XMLHttpRequest();
    xhr.open("POST", path);
    xhr.withCredentials = true;
    for (const [key, value] of Object.entries(options.headers ?? {})) xhr.setRequestHeader(key, value);
    if (csrfToken) xhr.setRequestHeader(csrfHeader, csrfToken);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) options.onProgress?.(event.loaded / event.total);
    };
    xhr.onerror = () => reject(new ApiError(0, "network_error", "Network request failed"));
    xhr.onabort = () => reject(new DOMException("Aborted", "AbortError"));
    xhr.onload = () => {
      if (xhr.status < 200 || xhr.status >= 300) {
        let response: Response;
        try {
          response = new Response(xhr.responseText, { status: xhr.status });
        } catch {
          // A status that cannot carry a body (304) or is outside 200-599 makes the constructor throw.
          reject(new ApiError(xhr.status, "invalid_response", `Unexpected ${xhr.status} response`));
          return;
        }
        void toApiError(response).then(reject);
        return;
      }
      try {
        resolve((xhr.responseText === "" ? undefined : parseJson(xhr.responseText)) as T);
      } catch {
        reject(new ApiError(xhr.status, "invalid_response", "Response body is not valid JSON"));
      }
    };
    options.signal?.addEventListener("abort", () => xhr.abort());
    xhr.send(body);
  });
}

export async function requestBlob(path: string, options: RequestOptions = {}): Promise<Blob> {
  const response = await send("GET", path, options);
  return response.blob();
}
