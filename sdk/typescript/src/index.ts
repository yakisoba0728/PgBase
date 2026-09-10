export * from "./admin.js";
export type User = { id: string; email: string; createdAt: string; verified?: boolean; authCollectionId?: string; authCollection?: string };
export type UserSession = { token?: string; user: User; expiresAt: string; impersonated?: boolean; impersonatingAdministratorId?: string };
export type GeoPoint = { lon: number; lat: number };
export type RecordData = Record<string, unknown>;
export type RecordPage<T extends RecordData> = { items: T[]; nextCursor?: string };
export type RecordResult<T extends RecordData> = { record: T; etag?: string };
export type Query = {
  filter?: string;
  sort?: string;
  expand?: string;
  /** Comma-separated record fields to return. */
  fields?: string;
  cursor?: string;
  limit?: number;
};
/** Options accepted by a single-record read. */
export type RecordGetOptions = Pick<Query, "expand" | "fields">;
/** Options accepted by record mutations. */
export type RecordWriteOptions = Pick<Query, "fields">;
export type BatchOptions = Pick<Query, "fields">;
export type RecordEvent = {
  /** Durable outbox event UUID, distinct from the SSE delivery cursor. */
  eventId: string;
  /** Monotonic decimal SSE delivery cursor. */
  deliveryId: string;
  collection: string;
  recordId: string;
  operation: string;
  version: number;
};
export type EventOptions = {
  lastEventId?: string | number;
  signal?: AbortSignal;
  maxReconnects?: number;
  reconnectDelayMs?: number;
};
export type RequestOptions = { signal?: AbortSignal; requestKey?: string };
/** Opens the provider URL using a browser popup, redirect, or mobile launcher. */
export type OAuthLaunchOptions = { open(url: string): void | Promise<void> };
export type BatchOperation<T extends RecordData = RecordData> =
  | { operation: "create"; collection: string; input: Omit<T, "id">; idempotencyKey?: string }
  | { operation: "update"; collection: string; id: string; input: Partial<T>; ifMatch?: string }
  | BatchUpsertOperation<T>
  | { operation: "delete"; collection: string; id: string; ifMatch?: string };
/** An atomic PUT-equivalent batch operation: creates the ID when absent, otherwise updates it. */
export type BatchUpsertOperation<T extends RecordData = RecordData> = {
  operation: "upsert";
  collection: string;
  id: string;
  input: Partial<T>;
  ifMatch?: string;
};
export type BatchResult<T extends RecordData = RecordData> = { eventId: string; record?: T; etag?: string; version?: number };
export type BatchHTTPRequest<T extends RecordData = RecordData> = { method: "POST" | "PATCH" | "PUT" | "DELETE"; url: string; body?: Partial<T>; headers?: Record<string,string> };
export type BatchHTTPResult<T extends RecordData = RecordData> = { status: number; headers?: Record<string,string>; body?: T; eventId: string; etag?: string; version?: number };
export type BatchFilePart = { blob: Blob; filename: string };
export type FileInfo = { id: string; field: string; filename: string; contentType: string; size: number; thumbnail: boolean };
export type MFAChallenge = { challengeToken: string; expiresAt: string };
/** Enabled sign-in methods for one explicit public auth collection. */
export type AuthMethods = {
  passwordEnabled: boolean;
  otpEnabled: boolean;
  mfaEnabled: boolean;
  oauthProviders: string[];
};

export class PgBaseError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly details: Record<string, string[]> = {},
    readonly etag?: string,
  ) {
    super(code);
    this.name = "PgBaseError";
  }
}

export class MFAChallengeError extends PgBaseError {
  constructor(status: number, code: string, details: Record<string, string[]>, readonly challenge: MFAChallenge) {
    super(status, code, details);
    this.name = "MFAChallengeError";
  }
}

export interface TokenStorage {
  get(): string | null;
  set(token: string): void;
  clear(): void;
}

/** A serialized PocketBase-compatible auth state for SSR hand-off. */
export type AuthState = { token: string | null; record: RecordData | null };

export class AuthStore {
  #token: string | null;
  #record: RecordData | null = null;
  #generation = 0;
  #listeners = new Set<(generation: number) => void>();

  constructor(private readonly storage?: TokenStorage) {
    this.#token = storage?.get() ?? null;
  }

  get token(): string | null {
    return this.#token;
  }
  get record(): RecordData | null { return this.#record; }
  get isValid(): boolean { return this.#token !== null && this.#token.length > 0; }

  get generation(): number {
    return this.#generation;
  }

  save(token: string, record: RecordData | null = null): void {
    this.#token = token;
    this.#record = record;
    this.#generation += 1;
    this.storage?.set(token);
    this.notify();
  }

  clear(): void {
    this.#token = null;
    this.#record = null;
    this.#generation += 1;
    this.storage?.clear();
    this.notify();
  }

  /** Encodes token and cached auth record for a server-rendered response cookie. */
  exportToCookie(options: { key?: string; httpOnly?: boolean; secure?: boolean; sameSite?: "Lax" | "Strict" | "None"; path?: string } = {}): string {
    const key = options.key ?? "pb_auth";
    const path = options.path ?? "/";
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(key) || /[;\r\n]/.test(path)) throw new PgBaseError(0, "invalid_cookie_option");
    const payload = encodeURIComponent(JSON.stringify({ token: this.#token, record: this.#record }));
    return [`${key}=${payload}`, `Path=${path}`, options.httpOnly === false ? "" : "HttpOnly", options.secure ? "Secure" : "", options.sameSite ? `SameSite=${options.sameSite}` : ""].filter(Boolean).join("; ");
  }

  /** Loads a state exported by [exportToCookie]; malformed cookies clear state. */
  loadFromCookie(cookie: string, key = "pb_auth"): void {
    const encoded = cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${key}=`))?.slice(key.length + 1);
    if (!encoded) return;
    try {
      const value = JSON.parse(decodeURIComponent(encoded)) as Partial<AuthState>;
      if ((value.token !== null && typeof value.token !== "string") || (value.record !== null && (typeof value.record !== "object" || Array.isArray(value.record)))) throw new Error("invalid auth cookie");
      this.#token = value.token ?? null;
      this.#record = (value.record ?? null) as RecordData | null;
      this.#generation += 1;
      this.notify();
    } catch { this.clear(); }
  }

  _onChange(listener: (generation: number) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  private notify(): void {
    for (const listener of this.#listeners) {
      listener(this.#generation);
    }
  }
}

/**
 * Wrap browser storage only when the caller deliberately supplies it. This
 * keeps token persistence opt-in and makes the storage lifecycle visible at
 * client construction.
 */
export function browserTokenStorage(storage: Storage, key = "pgbase.user.token"): TokenStorage {
  return {
    get: () => storage.getItem(key),
    set: (token) => storage.setItem(key, token),
    clear: () => storage.removeItem(key),
  };
}

export class PgBaseClient {
  readonly auth: AuthStore;
  private readonly fetcher: typeof fetch;
  private readonly requestControllers = new Map<string, AbortController>();
  /** Matches PocketBase's opt-out switch for duplicate request cancellation. */
  autoCancellation = true;

  constructor(
    readonly baseURL: string,
    options: { fetch?: typeof fetch; auth?: AuthStore } = {},
  ) {
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.auth = options.auth ?? new AuthStore();
  }

  register(email: string, password: string, signal?: AbortSignal): Promise<UserSession> {
    return this.authenticate("/api/v1/auth/register", email, password, signal);
  }

  /** Binds authentication calls to an explicit auth realm without changing record APIs. */
  realm(name: string): AuthRealm {
    return new AuthRealm(this, name);
  }

  login(email: string, password: string, signal?: AbortSignal): Promise<UserSession> {
    return this.authenticate("/api/v1/auth/login", email, password, signal).catch((error) => {
      if (error instanceof PgBaseError && error.code === "mfa_required") {
        const [challengeToken] = error.details.challengeToken ?? [];
        const [expiresAt] = error.details.expiresAt ?? [];
        if (challengeToken && expiresAt) throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
      }
      throw error;
    });
  }

  session(signal?: AbortSignal): Promise<UserSession> {
    return this.request("/api/v1/auth/session", {}, signal);
  }

  async logout(signal?: AbortSignal): Promise<void> {
    try {
      await this.request<void>("/api/v1/auth/logout", { method: "POST" }, signal);
    } finally {
      this.auth.clear();
    }
  }

  async changePassword(
    currentPassword: string,
    newPassword: string,
    signal?: AbortSignal,
  ): Promise<void> {
    try {
      await this.request<void>("/api/v1/auth/change-password", {
        method: "POST",
        body: JSON.stringify({ currentPassword, newPassword }),
      }, signal);
    } finally {
      this.auth.clear();
    }
  }

  requestPasswordReset(email: string, signal?: AbortSignal): Promise<void> {
    return this.request<void>("/api/v1/auth/request-password-reset", {
      method: "POST",
      body: JSON.stringify({ email }),
    }, signal);
  }

  requestOTP(email: string, signal?: AbortSignal): Promise<{ otpId: string; expiresAt: string }> {
    return this.request("/api/v1/auth/otp/request", { method: "POST", body: JSON.stringify({ email }) }, signal);
  }

  async completeOTP(otpId: string, code: string, signal?: AbortSignal): Promise<UserSession> {
    const generation = this.auth.generation;
    try { const session = await this.request<UserSession>("/api/v1/auth/otp/complete", { method: "POST", body: JSON.stringify({ otpId, code }) }, signal); if (session.token && generation === this.auth.generation) this.auth.save(session.token, session.user); return session; }
    catch (error) { if (error instanceof PgBaseError && error.code === "mfa_required") { const [challengeToken] = error.details.challengeToken ?? []; const [expiresAt] = error.details.expiresAt ?? []; if (challengeToken && expiresAt) throw new MFAChallengeError(error.status, error.code,error.details,{challengeToken,expiresAt}); } throw error; }
  }

  confirmPasswordReset(token: string, newPassword: string, signal?: AbortSignal): Promise<void> {
    return this.request<void>("/api/v1/auth/confirm-password-reset", {
      method: "POST",
      body: JSON.stringify({ token, newPassword }),
    }, signal);
  }

  requestVerification(signal?: AbortSignal): Promise<void> {
    return this.request<void>("/api/v1/auth/request-verification", { method: "POST" }, signal);
  }

  confirmVerification(token: string, signal?: AbortSignal): Promise<void> {
    return this.request<void>("/api/v1/auth/confirm-verification", {
      method: "POST",
      body: JSON.stringify({ token }),
    }, signal);
  }

  requestEmailChange(
    currentPassword: string,
    newEmail: string,
    signal?: AbortSignal,
  ): Promise<void> {
    return this.request<void>("/api/v1/auth/request-email-change", {
      method: "POST",
      body: JSON.stringify({ currentPassword, newEmail }),
    }, signal);
  }

  async confirmEmailChange(token: string, signal?: AbortSignal): Promise<void> {
    try {
      await this.request<void>("/api/v1/auth/confirm-email-change", {
        method: "POST",
        body: JSON.stringify({ token }),
      }, signal);
    } finally {
      this.auth.clear();
    }
  }

  records<T extends RecordData = RecordData>(collection: string): Records<T> {
    return new Records<T>(this, collection);
  }

  batch<T extends RecordData = RecordData>(operations: BatchOperation<T>[], options?: BatchOptions | AbortSignal, signal?: AbortSignal): Promise<{ results: BatchResult<T>[] }> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ operations }) }, requestSignal);
  }

  batchRequests<T extends RecordData = RecordData>(requests: BatchHTTPRequest<T>[], options?: BatchOptions | AbortSignal, signal?: AbortSignal): Promise<{ results: BatchHTTPResult<T>[] }> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ requests }) }, requestSignal);
  }

  batchMultipart<T extends RecordData = RecordData>(requests: BatchHTTPRequest<T>[], files: Record<string,BatchFilePart>, signal?: AbortSignal): Promise<{ results: BatchHTTPResult<T>[] }> {
    const body=new FormData();body.append("requests",JSON.stringify({requests}));
    for(const [key,file] of Object.entries(files)){if(key==="requests"||key===""||file.filename==="")throw new PgBaseError(0,"invalid_batch_file");body.append(key,file.blob,file.filename)}
    return this.request("/api/v1/user/collections/batch",{method:"POST",body},signal);
  }

  files(collection: string, recordID: string): Files {
    return new Files(this, collection, recordID);
  }

  async completeMFA(challengeToken: string, code: string, signal?: AbortSignal): Promise<UserSession> {
    const generation = this.auth.generation;
    const session = await this.request<UserSession>("/api/v1/auth/mfa/complete", { method: "POST", body: JSON.stringify({ challengeToken, code }) }, signal);
    if (session.token && generation === this.auth.generation) this.auth.save(session.token, session.user);
    return session;
  }

  beginTOTPEnrollment(currentPassword: string, signal?: AbortSignal): Promise<{ secret: string; otpAuthUrl: string }> {
    return this.request("/api/v1/auth/totp/enroll", { method: "POST", body: JSON.stringify({ currentPassword }) }, signal);
  }

  confirmTOTPEnrollment(code: string, signal?: AbortSignal): Promise<{ codes: string[] }> {
    return this.request("/api/v1/auth/totp/confirm", { method: "POST", body: JSON.stringify({ code }) }, signal);
  }

  async *iterate<T extends RecordData>(
    collection: string,
    query: Query = {},
    signal?: AbortSignal,
  ): AsyncGenerator<T> {
    let cursor = query.cursor;
    do {
      const page = await this.records<T>(collection).list({ ...query, cursor }, signal);
      yield* page.items;
      cursor = page.nextCursor;
    } while (cursor);
  }

  _request<T>(path: string, init?: RequestInit, signal?: AbortSignal): Promise<T> {
    return this.request(path, init, signal);
  }

  /** Cancels an in-flight request started with this key. */
  cancelRequest(requestKey: string): void { this.requestControllers.get(requestKey)?.abort(); }
  /** Cancels every keyed in-flight request. */
  cancelAllRequests(): void { for (const controller of this.requestControllers.values()) controller.abort(); this.requestControllers.clear(); }

  /** Low-level request with PocketBase-style request-key auto-cancellation. */
  async send<T>(path: string, init: RequestInit = {}, options: RequestOptions = {}): Promise<T> {
    const keyed = this.keyedSignal(options.requestKey, options.signal);
    try { return await this.request(path, init, keyed.signal); }
    finally { keyed.cleanup(); }
  }

  async _response<T>(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<{ body: T; etag?: string }> {
    const response = await this.fetchResponse(path, init, signal);
    const body = await response.json().catch(() => undefined) as T;
    return { body, etag: response.headers.get("ETag") ?? undefined };
  }

  async _eventStream(path: string, lastEventId: string | undefined, signal?: AbortSignal): Promise<Response> {
    const headers = new Headers({ Accept: "text/event-stream" });
    if (this.auth.token) {
      headers.set("Authorization", `Bearer ${this.auth.token}`);
    }
    if (lastEventId !== undefined) {
      headers.set("Last-Event-ID", String(lastEventId));
    }
    const response = await this.fetcher(new URL(path, this.baseURL), { headers, signal });
    if (!response.ok) {
      const body = await response.json().catch(() => undefined) as Record<string, unknown> | undefined;
      const error = body?.error as { code?: string; details?: Record<string, string[]> } | undefined;
      throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined);
    }
    if (!response.body) {
      throw new PgBaseError(0, "event_stream_unavailable");
    }
    return response;
  }

  private async authenticate(
    path: string,
    email: string,
    password: string,
    signal?: AbortSignal,
  ): Promise<UserSession> {
    const generation = this.auth.generation;
    const session = await this.request<UserSession>(path, {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }, signal);
    if (session.token && generation === this.auth.generation) {
      this.auth.save(session.token, session.user);
    }
    return session;
  }

  private async request<T>(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<T> {
	const response = await this.fetchResponse(path, init, signal);
	return await response.json().catch(() => undefined) as T;
  }

  private async fetchResponse(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<Response> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (this.auth.token) {
      headers.set("Authorization", `Bearer ${this.auth.token}`);
    }
    if (init.body !== undefined && !(init.body instanceof FormData)) {
      headers.set("Content-Type", "application/json");
    }

    const response = await this.fetcher(new URL(path, this.baseURL), { ...init, headers, signal });
    if (!response.ok) {
	  const body = await response.json().catch(() => undefined) as Record<string, unknown> | undefined;
      const error = body?.error as { code?: string; details?: Record<string, string[]> } | undefined;
      throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined);
    }
	return response;
  }

  private keyedSignal(key?: string, signal?: AbortSignal): { signal: AbortSignal | undefined; cleanup: () => void } {
    if (!key || !this.autoCancellation) return { signal, cleanup: () => {} };
    if (this.autoCancellation) this.cancelRequest(key);
    const controller = new AbortController();
    this.requestControllers.set(key, controller);
    if (signal?.aborted) controller.abort();
    else signal?.addEventListener("abort", () => controller.abort(), { once: true });
    return { signal: controller.signal, cleanup: () => { if (this.requestControllers.get(key) === controller) this.requestControllers.delete(key); } };
  }
}

export class AuthRealm {
  constructor(private readonly client: PgBaseClient, readonly name: string) {}
  private path(action: string): string { return `/api/v1/auth/${encodeURIComponent(this.name)}/${action}`; }
  private async authenticate(action: "register" | "login", email: string, password: string, signal?: AbortSignal): Promise<UserSession> {
    const generation = this.client.auth.generation;
    try {
      const session = await this.client._request<UserSession>(this.path(action), { method: "POST", body: JSON.stringify({ email, password }) }, signal);
      if (session.token && generation === this.client.auth.generation) this.client.auth.save(session.token, session.user);
      return session;
    } catch (error) {
      if (error instanceof PgBaseError && error.code === "mfa_required") {
        const [challengeToken] = error.details.challengeToken ?? []; const [expiresAt] = error.details.expiresAt ?? [];
        if (challengeToken && expiresAt) throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
      }
      throw error;
    }
  }
  register(email: string, password: string, signal?: AbortSignal): Promise<UserSession> { return this.authenticate("register", email, password, signal); }
  login(email: string, password: string, signal?: AbortSignal): Promise<UserSession> { return this.authenticate("login", email, password, signal); }
  /** Returns the sign-in methods enabled for this auth collection. */
  methods(signal?: AbortSignal): Promise<AuthMethods> { return this.client._request(this.path("methods"), {}, signal); }
  /** Rotates the current bearer token after validating its replacement session. */
  async refresh(signal?: AbortSignal): Promise<UserSession> {
    const generation = this.client.auth.generation;
    const session = refreshSession(await this.client._request<unknown>(this.path("refresh"), { method: "POST" }, signal));
    if (generation === this.client.auth.generation) this.client.auth.save(session.token, session.user);
    return session;
  }
  session(signal?: AbortSignal): Promise<UserSession> { return this.client._request(this.path("session"), {}, signal); }
  async logout(signal?: AbortSignal): Promise<void> { try { await this.client._request<void>(this.path("logout"), { method: "POST" }, signal); } finally { this.client.auth.clear(); } }
  requestPasswordReset(email: string, signal?: AbortSignal): Promise<void> { return this.client._request(this.path("request-password-reset"), { method: "POST", body: JSON.stringify({ email }) }, signal); }
  requestOTP(email: string, signal?: AbortSignal): Promise<{ otpId: string; expiresAt: string }> { return this.client._request(this.path("otp/request"), { method: "POST", body: JSON.stringify({ email }) }, signal); }
  async completeOTP(otpId: string, code: string, signal?: AbortSignal): Promise<UserSession> {
    const generation = this.client.auth.generation;
    try { const session = await this.client._request<UserSession>(this.path("otp/complete"), { method: "POST", body: JSON.stringify({ otpId, code }) }, signal); if (session.token && generation === this.client.auth.generation) this.client.auth.save(session.token, session.user); return session; }
    catch (error) { if (error instanceof PgBaseError && error.code === "mfa_required") { const [challengeToken] = error.details.challengeToken ?? []; const [expiresAt] = error.details.expiresAt ?? []; if (challengeToken && expiresAt) throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt }); } throw error; }
  }
  async profile<T extends RecordData = RecordData>(options?: RecordGetOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    const [query, requestSignal] = recordGetArguments(options, signal);
    const parameters = new URLSearchParams();
    if (query?.expand !== undefined) parameters.set("expand", query.expand);
    if (query?.fields !== undefined) parameters.set("fields", query.fields);
    return (await this.client._request<{ attributes: T }>(`${this.path("profile")}${parameters.size ? `?${parameters}` : ""}`, {}, requestSignal)).attributes;
  }
  async updateProfile<T extends RecordData = RecordData>(profile: T, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    const suffix = query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`;
    return (await this.client._request<{ attributes: T }>(`${this.path("profile")}${suffix}`, { method: "PATCH", body: JSON.stringify(profile) }, requestSignal)).attributes;
  }
  oauthStart(provider: string, redirectUri: string, signal?: AbortSignal): Promise<{ authorizationUrl: string; expiresAt: string }> { return this.client._request(this.path("oauth/start"), { method: "POST", body: JSON.stringify({ provider, redirectUri }) }, signal); }
  /** Starts OAuth and delegates browser/popup/deep-link navigation to the host. */
  async oauthStartAndOpen(provider: string, redirectUri: string, options: OAuthLaunchOptions, signal?: AbortSignal): Promise<{ authorizationUrl: string; expiresAt: string }> {
    const started = await this.oauthStart(provider, redirectUri, signal);
    await options.open(started.authorizationUrl);
    return started;
  }
  oauthLink(currentPassword: string, provider: string, redirectUri: string, signal?: AbortSignal): Promise<{ authorizationUrl: string; expiresAt: string }> { return this.client._request("/api/v1/auth/oauth/link", { method: "POST", body: JSON.stringify({ currentPassword, provider, redirectUri }) }, signal); }
  async oauthComplete(provider: string, state: string, code: string, signal?: AbortSignal): Promise<UserSession> { const generation = this.client.auth.generation; const session = await this.client._request<UserSession>("/api/v1/auth/oauth/complete", { method: "POST", body: JSON.stringify({ provider, state, code }) }, signal); if (session.token && generation === this.client.auth.generation) this.client.auth.save(session.token, session.user); return session; }
  oauthCompleteFromURL(provider: string, callbackURL: string | URL, signal?: AbortSignal): Promise<UserSession> {
    const url = typeof callbackURL === "string" ? new URL(callbackURL) : callbackURL;
    const providerError = url.searchParams.get("error");
    if (providerError) throw new OAuthCallbackError(providerError, url.searchParams.get("error_description") ?? undefined);
    const state = url.searchParams.get("state"); const code = url.searchParams.get("code");
    if (!state || !code) throw new OAuthCallbackError("invalid_callback", "The OAuth callback is missing state or code.");
    return this.oauthComplete(provider, state, code, signal);
  }
}

/** Refresh responses always contain a replacement token, unlike session reads. */
function refreshSession(value: unknown): UserSession & { token: string } {
  if (!value || typeof value !== "object") throw new PgBaseError(0, "invalid_session_response");
  const session = value as Record<string, unknown>;
  const user = session.user;
  if (typeof session.token !== "string" || session.token.length === 0 || typeof session.expiresAt !== "string" || !user || typeof user !== "object") {
    throw new PgBaseError(0, "invalid_session_response");
  }
  const details = user as Record<string, unknown>;
  if (typeof details.id !== "string" || typeof details.email !== "string" || typeof details.createdAt !== "string") {
    throw new PgBaseError(0, "invalid_session_response");
  }
  if (details.verified !== undefined && typeof details.verified !== "boolean") throw new PgBaseError(0, "invalid_session_response");
  if (details.authCollection !== undefined && typeof details.authCollection !== "string") throw new PgBaseError(0, "invalid_session_response");
  if (details.authCollectionId !== undefined && typeof details.authCollectionId !== "string") throw new PgBaseError(0, "invalid_session_response");
  return { token: session.token, expiresAt: session.expiresAt, user: details as User };
}

export class OAuthCallbackError extends Error {
  constructor(readonly code: string, message?: string) { super(message ?? code); this.name = "OAuthCallbackError"; }
}

export class Records<T extends RecordData> {
  constructor(
    private readonly client: PgBaseClient,
    private readonly collection: string,
  ) {}

  list(query: Query = {}, signal?: AbortSignal): Promise<RecordPage<T>> {
    const parameters = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined) {
        parameters.set(key, String(value));
      }
    }
    const suffix = parameters.size ? `?${parameters}` : "";
    return this.client._request(`${this.path()}${suffix}`, {}, signal);
  }

  get(id: string, signal?: AbortSignal): Promise<T>;
  get(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<T>;
  async get(id: string, optionsOrSignal?: RecordGetOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
    return (await this.getWithETag(id, options, requestSignal)).record;
  }

  getWithETag(id: string, signal?: AbortSignal): Promise<RecordResult<T>>;
  getWithETag(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<RecordResult<T>>;
  getWithETag(id: string, optionsOrSignal?: RecordGetOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>> {
    const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
    return this.recordResponse(this.path(id, options), {}, requestSignal);
  }

  async create(data: Omit<T, "id">, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    return (await this.createWithETag(data, idempotencyKey, options, signal)).record;
  }

  createWithETag(data: Omit<T, "id">, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.recordResponse(this.path(undefined, query), {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: JSON.stringify(data),
    }, requestSignal);
  }

  async update(id: string, data: Partial<T>, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    return (await this.updateWithETag(id, data, etag, options, signal)).record;
  }

  updateWithETag(id: string, data: Partial<T>, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.recordResponse(this.path(id, query), {
      method: "PATCH",
      headers: { "If-Match": etag },
      body: JSON.stringify(data),
    }, requestSignal);
  }

  remove(id: string, etag: string, signal?: AbortSignal): Promise<void> {
    return this.client._request(this.path(id), {
      method: "DELETE",
      headers: { "If-Match": etag },
    }, signal);
  }

  async *events(options: EventOptions = {}): AsyncGenerator<RecordEvent> {
    const generation = this.client.auth.generation;
    const controller = new AbortController();
    const stopOnAuthChange = this.client.auth._onChange((nextGeneration) => {
      if (nextGeneration !== generation) {
        controller.abort();
      }
    });
    options.signal?.addEventListener("abort", () => controller.abort(), { once: true });
    const maximumReconnects = options.maxReconnects ?? 5;
    const initialDelay = options.reconnectDelayMs ?? 250;
    let reconnects = 0;
    let lastEventId = options.lastEventId === undefined ? undefined : canonicalDeliveryCursor(options.lastEventId);
    const deliveredEventIDs = new Set<string>();

    try {
      while (!controller.signal.aborted && generation === this.client.auth.generation) {
        try {
          const response = await this.client._eventStream(this.eventsPath(), lastEventId, controller.signal);
          for await (const message of parseEventStream(response.body!, controller.signal)) {
            if (generation !== this.client.auth.generation || controller.signal.aborted) {
              return;
            }
            const deliveryId = deliveryCursor(message);
            if (deliveryId === undefined || (lastEventId !== undefined && BigInt(deliveryId) <= BigInt(lastEventId))) {
              continue;
            }
            // Cursor messages move a resume position even when the server did
            // not expose a matching record to this user.
            lastEventId = deliveryId;
            if (message.event === "cursor") {
              continue;
            }
            const event = recordEvent(message, deliveryId);
            if (!event || deliveredEventIDs.has(event.eventId)) {
              continue;
            }
            deliveredEventIDs.add(event.eventId);
            if (deliveredEventIDs.size > 1_000) {
              deliveredEventIDs.delete(deliveredEventIDs.values().next().value!);
            }
            reconnects = 0;
            yield event;
          }
        } catch (error) {
          if (controller.signal.aborted || generation !== this.client.auth.generation) {
            return;
          }
          if (reconnects >= maximumReconnects) {
            throw error;
          }
        }

        reconnects += 1;
        const delay = Math.min(initialDelay * 2 ** (reconnects - 1), 10_000);
        await waitForReconnect(delay, controller.signal);
      }
    } finally {
      stopOnAuthChange();
    }
  }

  private path(id?: string, query?: RecordGetOptions): string {
    const base = `/api/v1/user/collections/${encodeURIComponent(this.collection)}/records`;
    if (!id) {
      const parameters = new URLSearchParams();
      if (query?.fields !== undefined) parameters.set("fields", query.fields);
      return parameters.size ? `${base}?${parameters}` : base;
    }
    const parameters = new URLSearchParams();
    if (query?.expand !== undefined) parameters.set("expand", query.expand);
    if (query?.fields !== undefined) parameters.set("fields", query.fields);
    const suffix = parameters.size ? `?${parameters}` : "";
    return `${base}/${encodeURIComponent(id)}${suffix}`;
  }

  private eventsPath(): string {
    return `/api/v1/user/collections/${encodeURIComponent(this.collection)}/events`;
  }

  private async recordResponse(path: string, init: RequestInit, signal?: AbortSignal): Promise<RecordResult<T>> {
    const response = await this.client._response<T>(path, init, signal);
    return { record: response.body, etag: response.etag };
  }
}

function recordGetArguments(
  optionsOrSignal?: RecordGetOptions | AbortSignal,
  signal?: AbortSignal,
): [RecordGetOptions | undefined, AbortSignal | undefined] {
  if (optionsOrSignal && "aborted" in optionsOrSignal) return [undefined, optionsOrSignal];
  return [optionsOrSignal as RecordGetOptions | undefined, signal];
}

function canonicalDeliveryCursor(value: string | number): string {
  if (typeof value === "number" && !Number.isSafeInteger(value)) throw new PgBaseError(0, "invalid_cursor");
  const text = String(value);
  if (!/^\d+$/.test(text)) throw new PgBaseError(0, "invalid_cursor");
  const cursor = BigInt(text);
  if (cursor > 9_223_372_036_854_775_807n) throw new PgBaseError(0, "invalid_cursor");
  return cursor.toString();
}

export class Files {
  constructor(private readonly client: PgBaseClient, private readonly collection: string, private readonly recordID: string) {}

  list(field: string, signal?: AbortSignal): Promise<{ items: FileInfo[] }> {
    return this.client._request(this.fieldPath(field), {}, signal);
  }

  async upload(field: string, file: Blob, filename: string, signal?: AbortSignal): Promise<FileInfo> {
    const bytes = await file.arrayBuffer();
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    const body = new FormData();
    body.append("file", file, filename);
    return this.client._request(this.fieldPath(field), {
      method: "POST",
      headers: {
        "X-PgBase-File-Size": String(file.size),
        "X-PgBase-File-SHA256": hex(new Uint8Array(digest)),
      },
      body,
    }, signal);
  }

  delete(fileID: string, signal?: AbortSignal): Promise<void> {
    return this.client._request(this.filePath(fileID), { method: "DELETE" }, signal);
  }

  mintDownloadToken(fileID: string, variant = "original", signal?: AbortSignal): Promise<{ token: string }> {
    return this.client._request(`${this.filePath(fileID)}/token`, { method: "POST", body: JSON.stringify({ variant }) }, signal);
  }

  downloadURL(fileID: string, token: string, variant = "original"): string {
    const url = new URL(this.filePath(fileID), this.client.baseURL);
    url.searchParams.set("token", token);
    if (variant !== "original") url.searchParams.set("variant", variant);
    return url.toString();
  }

  private fieldPath(field: string): string {
    return `/api/v1/user/collections/${encodeURIComponent(this.collection)}/records/${encodeURIComponent(this.recordID)}/files/${encodeURIComponent(field)}`;
  }

  private filePath(fileID: string): string {
    return `/api/v1/user/files/${encodeURIComponent(fileID)}`;
  }
}

function hex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

type SSEMessage = { event?: string; id?: string; data: string };

async function* parseEventStream(
  body: ReadableStream<Uint8Array>,
  signal?: AbortSignal,
): AsyncGenerator<SSEMessage> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffered = "";
  try {
    while (!signal?.aborted) {
      const { done, value } = await reader.read();
      buffered += decoder.decode(value, { stream: !done });
      const messages = buffered.split(/\r?\n\r?\n/);
      buffered = messages.pop() ?? "";
      for (const raw of messages) {
        const message = parseSSEMessage(raw);
        if (message) {
          yield message;
        }
      }
      if (done) {
        break;
      }
    }
  } finally {
    reader.releaseLock();
  }
}

function parseSSEMessage(raw: string): SSEMessage | undefined {
  const message: SSEMessage = { data: "" };
  for (const line of raw.split(/\r?\n/)) {
    if (line.startsWith("event:")) {
      message.event = line.slice(6).trim();
    } else if (line.startsWith("id:")) {
      message.id = line.slice(3).trim();
    } else if (line.startsWith("data:")) {
      message.data += `${message.data ? "\n" : ""}${line.slice(5).trimStart()}`;
    }
  }
  return message.data ? message : undefined;
}

function deliveryCursor(message: SSEMessage): string | undefined {
  if (!message.id || !/^\d+$/.test(message.id)) {
    return undefined;
  }
  try {
    const cursor = BigInt(message.id);
    return cursor <= 9_223_372_036_854_775_807n ? cursor.toString() : undefined;
  } catch {
    return undefined;
  }
}

function recordEvent(message: SSEMessage, deliveryId: string): RecordEvent | undefined {
  if (message.event !== "record") {
    return undefined;
  }
  try {
    const value = JSON.parse(message.data) as Omit<RecordEvent, "deliveryId">;
    if (!value.eventId || typeof value.eventId !== "string") {
      return undefined;
    }
    return { ...value, deliveryId };
  } catch {
    return undefined;
  }
}

function waitForReconnect(delay: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, delay);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      resolve();
    }, { once: true });
  });
}
