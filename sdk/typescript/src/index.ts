export * from "./types.js";
export * from "./auth-store.js";
export { RealtimeService } from "./realtime.js";
import { AuthStore } from "./auth-store.js";
import { RealtimeService } from "./realtime.js";
import { PgBaseError, MFAChallengeError } from "./types.js";
import type { AuthCreationOptions } from "./types.js";
import type { User, UserSession, GeoPoint, RecordData, FilterParams, RecordPage, PocketBaseListOptions, PocketBasePage, RecordModifiers, RecordMutation, RecordResult, RecordJSONResult, Query, RecordGetOptions, RecordWriteOptions, BatchOptions, RecordEvent, EventOptions, RealtimeSubscriptionOptions, RealtimeEvent, RealtimeCustomEvent, RealtimeSubscriptionEvent, RequestOptions, OAuthLaunchOptions, OAuthBrowserAdapter, BatchOperation, BatchUpsertOperation, BatchResult, BatchHTTPRequest, BatchHTTPResult, BatchFilePart, FileInfo, MFAChallenge, AuthMethods, OAuthStart } from "./types.js";
import { parseEventStream, canonicalDeliveryCursor, deliveryCursor, recordEvent, waitForReconnect } from "./sse.js";

export * from "./admin.js";
import { responseJSON } from "./response.js";
export { browserAuthStorage } from "./state-storage.js";
export type { AuthState, AuthStateStorage } from "./state-storage.js";
export class PgBaseClient {
  readonly auth: AuthStore;
  readonly realtime: RealtimeService;
  private readonly fetcher: typeof fetch;
  private readonly requestControllers = new Map<string, AbortController>();
  /** Cancel older reads with the same request URL, or requests with an explicit key. */
  autoCancellation = true;

  constructor(
    readonly baseURL: string,
    options: { fetch?: typeof fetch; auth?: AuthStore } = {},
  ) {
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.auth = options.auth ?? new AuthStore();
    this.realtime = new RealtimeService(this);
  }

  register<T extends RecordData = RecordData>(email: string, password: string, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<UserSession> {
    return this.authenticate("/api/v1/auth/register", email, password, options, signal);
  }

  /** Binds authentication calls to an explicit auth realm without changing record APIs. */
  realm(name: string): AuthRealm {
    return new AuthRealm(this, name);
  }

  login(email: string, password: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<UserSession> {
    return this.authenticate("/api/v1/auth/login", email, password, options, signal).catch((error) => {
      if (error instanceof PgBaseError && error.code === "mfa_required") {
        const [challengeToken] = error.details.challengeToken ?? [];
        const [expiresAt] = error.details.expiresAt ?? [];
        if (challengeToken && expiresAt) throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
      }
      throw error;
    });
  }

  session(options?: RecordGetOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>> {
    const [query, requestSignal] = recordGetArguments(options, signal);
    return this.request(authFieldsPath("/api/v1/auth/session", query), {}, requestSignal, query?.requestKey);
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

  requestOTP(email: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<{ otpId: string; expiresAt: string }>> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.request(authFieldsPath("/api/v1/auth/otp/request", query), { method: "POST", body: JSON.stringify({ email }) }, requestSignal, query?.requestKey);
  }

  async completeOTP(otpId: string, code: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    const generation = this.auth.generation;
    try { const session = await this.request<UserSession>(authFieldsPath("/api/v1/auth/otp/complete", query), { method: "POST", body: JSON.stringify({ otpId, code }) }, requestSignal, query?.requestKey); if (session.token && session.user && generation === this.auth.generation) this.auth.save(session.token, session.user); return session; }
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

  /**
   * Constructs a PocketBase filter expression without interpolating values.
   *
   * Parameters use PocketBase's `{:name}` placeholders.  Strings, dates,
   * arrays, and objects are emitted as quoted filter literals; scalar boolean,
   * number, and null values retain their filter-literal form.  A placeholder
   * without a matching parameter is deliberately left unchanged, matching the
   * PocketBase SDK and making an incomplete expression visible to the server.
   */
  filter(expression: string, params?: FilterParams): string {
    if (!params || Object.keys(params).length === 0) return expression;
    return expression.replace(/\{:([\w.-]+)\}/g, (placeholder, key: string) =>
      Object.prototype.hasOwnProperty.call(params, key) ? filterLiteral(params[key]) : placeholder,
    );
  }

  batch<T extends RecordData = RecordData>(operations: BatchOperation<T>[], options?: BatchOptions | AbortSignal, signal?: AbortSignal): Promise<{ results: BatchResult<T>[] }> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ operations }) }, requestSignal, query?.requestKey);
  }

  batchRequests<T extends RecordData = RecordData>(requests: BatchHTTPRequest<T>[], options?: BatchOptions | AbortSignal, signal?: AbortSignal): Promise<{ results: BatchHTTPResult<T>[] }> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ requests }) }, requestSignal, query?.requestKey);
  }

  batchMultipart<T extends RecordData = RecordData>(requests: BatchHTTPRequest<T>[], files: Record<string,BatchFilePart>, signal?: AbortSignal): Promise<{ results: BatchHTTPResult<T>[] }> {
    const body=new FormData();body.append("requests",JSON.stringify({requests}));
    for(const [key,file] of Object.entries(files)){if(key==="requests"||key===""||file.filename==="")throw new PgBaseError(0,"invalid_batch_file");body.append(key,file.blob,file.filename)}
    return this.request("/api/v1/user/collections/batch",{method:"POST",body},signal);
  }

  files(collection: string, recordID: string): Files {
    return new Files(this, collection, recordID);
  }

  async completeMFA(challengeToken: string, code: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    const generation = this.auth.generation;
    const session = await this.request<UserSession>(authFieldsPath("/api/v1/auth/mfa/complete", query), { method: "POST", body: JSON.stringify({ challengeToken, code }) }, requestSignal, query?.requestKey);
    if (session.token && session.user && generation === this.auth.generation) this.auth.save(session.token, session.user);
    return session;
  }

  beginTOTPEnrollment(currentPassword: string, signal?: AbortSignal): Promise<{ secret: string; otpAuthUrl: string }> {
    return this.request("/api/v1/auth/totp/enroll", { method: "POST", body: JSON.stringify({ currentPassword }) }, signal);
  }

  confirmTOTPEnrollment(code: string, signal?: AbortSignal): Promise<{ codes: string[] }> {
    return this.request("/api/v1/auth/totp/confirm", { method: "POST", body: JSON.stringify({ code }) }, signal);
  }

  disableTOTP(currentPassword: string, code: string, signal?: AbortSignal): Promise<void> {
    return this.request("/api/v1/auth/totp/disable", {
      method: "POST",
      body: JSON.stringify({ currentPassword, code }),
    }, signal);
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

  _request<T>(path: string, init?: RequestInit, signal?: AbortSignal, requestKey?: string | null): Promise<T> {
    return this.request(path, init, signal, requestKey);
  }

  /** Cancels an in-flight request started with this key. */
  cancelRequest(requestKey: string): void { this.requestControllers.get(requestKey)?.abort(); }
  /** Cancels every keyed in-flight request. */
  cancelAllRequests(): void { for (const controller of this.requestControllers.values()) controller.abort(); this.requestControllers.clear(); }

  /** Low-level request; null disables cancellation and a string supplies an explicit key. */
  async send<T>(path: string, init: RequestInit = {}, options: RequestOptions = {}): Promise<T> {
    return (await this.readResponse(path, init, options.signal, options.requestKey,
      (response) => responseJSON(response, () => { throw new PgBaseError(response.status, "invalid_response_body"); }))).body as T;
  }

  async _response<T>(path: string, init: RequestInit = {}, signal?: AbortSignal, requestKey?: string | null): Promise<{ body: T; etag?: string }> {
    return this.readResponse(path, init, signal, requestKey,
      async (response) => await responseJSON(response, () => { throw new PgBaseError(response.status, "invalid_response_body"); }) as T);
  }

  async _responseText(path: string, init: RequestInit = {}, signal?: AbortSignal, requestKey?: string | null): Promise<{ body: string; etag?: string }> {
    return this.readResponse(path, init, signal, requestKey, (response) => response.text());
  }

  private async readResponse<T>(path: string, init: RequestInit, signal: AbortSignal | undefined, requestKey: string | null | undefined, read: (response: Response) => Promise<T>): Promise<{ body: T; etag?: string }> {
    const method = (init.method ?? "GET").toUpperCase();
    const key = requestKey === undefined && (method === "GET" || method === "HEAD") ? `${method} ${path}` : requestKey;
    const keyed = this.keyedSignal(key, signal);
    try {
      const response = await this.fetchResponse(path, init, keyed.signal);
      return { body: await read(response), etag: response.headers.get("ETag") ?? undefined };
    } finally { keyed.cleanup(); }
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
      const body = await responseJSON(response, () => undefined) as Record<string, unknown> | undefined;
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
	options?: AuthCreationOptions | AbortSignal,
	signal?: AbortSignal,
	): Promise<UserSession> {
	const [query, requestSignal] = recordGetArguments(options, signal);
	const generation = this.auth.generation;
	const session = await this.request<UserSession>(`${path}${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, {
		method: "POST",
		body: JSON.stringify({ email, password, ...(path.endsWith("/register") && query?.profile !== undefined ? { fields: query.profile } : {}) }),
	}, requestSignal, query?.requestKey);
    if (session.token && generation === this.auth.generation) {
      this.auth.save(session.token, session.user);
    }
    return session;
  }

  private async request<T>(path: string, init: RequestInit = {}, signal?: AbortSignal, requestKey?: string | null): Promise<T> {
    return (await this._response<T>(path, init, signal, requestKey)).body;
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
	  const body = await responseJSON(response, () => undefined) as Record<string, unknown> | undefined;
      const error = body?.error as { code?: string; details?: Record<string, string[]> } | undefined;
      throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined);
    }
	return response;
  }

  private keyedSignal(key?: string | null, signal?: AbortSignal): { signal: AbortSignal | undefined; cleanup: () => void } {
    if (!key || !this.autoCancellation) return { signal, cleanup: () => {} };
    if (this.autoCancellation) this.cancelRequest(key);
    const controller = new AbortController();
    this.requestControllers.set(key, controller);
    const abort = () => controller.abort(signal?.reason);
    if (signal?.aborted) abort();
    else signal?.addEventListener("abort", abort, { once: true });
    return { signal: controller.signal, cleanup: () => {
      signal?.removeEventListener("abort", abort);
      if (this.requestControllers.get(key) === controller) this.requestControllers.delete(key);
    } };
  }
}

export class AuthRealm {
  constructor(private readonly client: PgBaseClient, readonly name: string) {}
  private path(action: string): string { return `/api/v1/auth/${encodeURIComponent(this.name)}/${action}`; }
  private async authenticate(action: "register" | "login", email: string, password: string, options?: AuthCreationOptions | AbortSignal, signal?: AbortSignal): Promise<UserSession> {
	const [query, requestSignal] = recordGetArguments(options, signal);
    const generation = this.client.auth.generation;
    try {
      const session = await this.client._request<UserSession>(`${this.path(action)}${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ email, password, ...(action === "register" && query?.profile !== undefined ? { fields: query.profile } : {}) }) }, requestSignal, query?.requestKey);
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
  register<T extends RecordData = RecordData>(email: string, password: string, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<UserSession> { return this.authenticate("register", email, password, options, signal); }
  login(email: string, password: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<UserSession> { return this.authenticate("login", email, password, options, signal); }
  /** Returns the sign-in methods enabled for this auth collection. */
  async methods(signal?: AbortSignal): Promise<AuthMethods> {
    const methods = await this.client._request<Omit<AuthMethods, "oauth2"> & { oauth2?: AuthMethods["oauth2"] }>(this.path("methods"), {}, signal);
    return { ...methods, oauth2: methods.oauth2 ?? { enabled: false, providers: [] } };
  }
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
    return (await this.client._request<{ attributes: T }>(`${this.path("profile")}${parameters.size ? `?${parameters}` : ""}`, {}, requestSignal, query?.requestKey)).attributes;
  }
  async updateProfile<T extends RecordData = RecordData>(profile: T, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    const suffix = query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`;
    return (await this.client._request<{ attributes: T }>(`${this.path("profile")}${suffix}`, { method: "PATCH", body: JSON.stringify(profile) }, requestSignal, query?.requestKey)).attributes;
  }
  oauthStart(provider: string, redirectUri: string, signal?: AbortSignal): Promise<OAuthStart> { return this.client._request(this.path("oauth/start"), { method: "POST", body: JSON.stringify({ provider, redirectUri }) }, signal); }
  /** Starts OAuth and delegates browser/popup/deep-link navigation to the host. */
  async oauthStartAndOpen(provider: string, redirectUri: string, options: OAuthLaunchOptions, signal?: AbortSignal): Promise<{ authorizationUrl: string; expiresAt: string }> {
    const started = await this.oauthStart(provider, redirectUri, signal);
    await options.open(started.authorizationUrl);
    return started;
  }
  async oauthWithBrowser<T extends RecordData = RecordData>(provider: string, redirectUri: string, adapter: OAuthBrowserAdapter, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<UserSession> {
    const [, requestSignal] = recordGetArguments(options, signal);
    try {
      const started = await this.oauthStart(provider, redirectUri, requestSignal);
      const expectedState = new URL(started.authorizationUrl).searchParams.get("state");
      if (!expectedState) throw new OAuthCallbackError("invalid_start", "The OAuth start URL is missing state.");
      await adapter.open(started.authorizationUrl);
      const callback = await callbackWithAbort(adapter, requestSignal);
      if (callback.origin !== new URL(redirectUri).origin) throw new OAuthCallbackError("invalid_callback_origin", "The OAuth callback origin does not match the redirect URL.");
      const providerError = callback.searchParams.get("error");
      if (providerError) throw new OAuthCallbackError(providerError, callback.searchParams.get("error_description") ?? undefined);
      const state = callback.searchParams.get("state"); const code = callback.searchParams.get("code");
      if (!state || !code) throw new OAuthCallbackError("invalid_callback", "The OAuth callback is missing state or code.");
      if (state !== expectedState) throw new OAuthCallbackError("invalid_state", "The OAuth callback state does not match the OAuth start state.");
      return await this.oauthComplete(provider, state, code, options, requestSignal) as UserSession;
    } finally { adapter.close?.(); }
  }
  oauthLink(currentPassword: string, provider: string, redirectUri: string, signal?: AbortSignal): Promise<OAuthStart> { return this.client._request("/api/v1/auth/oauth/link", { method: "POST", body: JSON.stringify({ currentPassword, provider, redirectUri }) }, signal); }
  async oauthComplete<T extends RecordData = RecordData>(provider: string, state: string, code: string, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>> { const [query, requestSignal] = recordGetArguments(options, signal); const generation = this.client.auth.generation; const session = await this.client._request<UserSession>(authFieldsPath("/api/v1/auth/oauth/complete", query), { method: "POST", body: JSON.stringify({ provider, state, code, ...(query?.profile === undefined ? {} : { fields: query.profile }) }) }, requestSignal, query?.requestKey); if (session.token && session.user && generation === this.client.auth.generation) this.client.auth.save(session.token, session.user); return session; }
  oauthCompleteFromURL<T extends RecordData = RecordData>(provider: string, callbackURL: string | URL, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>> {
    const url = typeof callbackURL === "string" ? new URL(callbackURL) : callbackURL;
    const providerError = url.searchParams.get("error");
    if (providerError) throw new OAuthCallbackError(providerError, url.searchParams.get("error_description") ?? undefined);
    const state = url.searchParams.get("state"); const code = url.searchParams.get("code");
    if (!state || !code) throw new OAuthCallbackError("invalid_callback", "The OAuth callback is missing state or code.");
    return this.oauthComplete(provider, state, code, options, signal);
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

function callbackWithAbort(adapter: OAuthBrowserAdapter, signal?: AbortSignal): Promise<URL> {
  const callback = adapter.waitForCallback(signal).then((value) => typeof value === "string" ? new URL(value) : value);
  if (!signal) return callback;
  if (signal.aborted) return Promise.reject(new DOMException("aborted", "AbortError"));
  return Promise.race([callback, new Promise<never>((_, reject) => signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true }))]);
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
    return this.client._request(this.listPath(query), {}, signal, query.requestKey);
  }

  /** Returns the complete { items, nextCursor? } JSON envelope without parsing numbers. */
  async listJSON(query: Query = {}, signal?: AbortSignal): Promise<string> {
    return (await this.client._responseText(this.listPath(query), {}, signal, query.requestKey)).body;
  }

  /**
   * Lists a numbered PocketBase-compatible page. This is intentionally
   * separate from [list], whose cursor contract remains native PgBase.
   */
  getList(page = 1, perPage = 30, options: PocketBaseListOptions = {}, signal?: AbortSignal): Promise<PocketBasePage<T>> {
    const parameters = new URLSearchParams({ compat: "pocketbase", page: String(page), perPage: String(perPage) });
    for (const [key, value] of Object.entries(options)) if (key !== "requestKey" && value !== undefined) parameters.set(key, String(value));
    return this.client._request(`${this.path()}?${parameters}`, {}, signal, options.requestKey);
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
    return this.recordResponse(this.path(id, options), {}, requestSignal, options?.requestKey);
  }

  /** Returns the complete record JSON and ETag without parsing numbers. */
  getJSON(id: string, signal?: AbortSignal): Promise<RecordJSONResult>;
  getJSON(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<RecordJSONResult>;
  getJSON(id: string, optionsOrSignal?: RecordGetOptions | AbortSignal, signal?: AbortSignal): Promise<RecordJSONResult> {
    const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
    return this.recordJSONResponse(this.path(id, options), {}, requestSignal, options?.requestKey);
  }

  async create(data: RecordMutation<T>, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    return (await this.createWithETag(data, idempotencyKey, options, signal)).record;
  }

  createWithETag(data: RecordMutation<T>, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.recordResponse(this.path(undefined, query), {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: JSON.stringify(data),
    }, requestSignal, query?.requestKey);
  }

  /** Sends a JSON object verbatim; retry an uncertain create with the same body and key. */
  createJSON(recordJSON: string, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordJSONResult> {
    const [query, requestSignal] = recordGetArguments(options, signal);
    return this.recordJSONResponse(this.path(undefined, query), {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: recordJSON,
    }, requestSignal, query?.requestKey);
  }

  async update(id: string, data: Partial<T> & RecordModifiers, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T> {
    return (await this.updateWithETag(id, data, etag, options, signal)).record;
  }

  updateWithETag(id: string, data: Partial<T> & RecordModifiers, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>> {
    const [query, requestSignal] = recordGetArguments(options as RecordGetOptions | AbortSignal | undefined, signal);
    return this.recordResponse(this.path(id, query), {
      method: "PATCH",
      headers: { "If-Match": etag },
      body: JSON.stringify(data),
    }, requestSignal, query?.requestKey);
  }

  /** Sends a record update JSON object verbatim with the current record ETag. */
  updateJSON(id: string, recordJSON: string, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordJSONResult> {
    const [query, requestSignal] = recordGetArguments(options, signal);
    return this.recordJSONResponse(this.path(id, query), {
      method: "PATCH",
      headers: { "If-Match": etag },
      body: recordJSON,
    }, requestSignal, query?.requestKey);
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
          const response = await this.client._eventStream(this.eventsPath(options.fields), lastEventId, controller.signal);
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

  private listPath(query: Query): string {
    const parameters = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (key !== "requestKey" && value !== undefined) parameters.set(key, String(value));
    }
    return `${this.path()}${parameters.size ? `?${parameters}` : ""}`;
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

  private eventsPath(fields?: string): string {
    const base = `/api/v1/user/collections/${encodeURIComponent(this.collection)}/events`;
    return fields === undefined ? base : `${base}?fields=${encodeURIComponent(fields)}`;
  }

  private async recordResponse(path: string, init: RequestInit, signal?: AbortSignal, requestKey?: string | null): Promise<RecordResult<T>> {
    const response = await this.client._response<T>(path, init, signal, requestKey);
    return { record: response.body, etag: response.etag };
  }

  private async recordJSONResponse(path: string, init: RequestInit, signal?: AbortSignal, requestKey?: string | null): Promise<RecordJSONResult> {
    const response = await this.client._responseText(path, init, signal, requestKey);
    return { recordJSON: response.body, etag: response.etag };
  }
}

function recordGetArguments<T extends RecordGetOptions = RecordGetOptions>(
  optionsOrSignal?: T | AbortSignal,
  signal?: AbortSignal,
): [T | undefined, AbortSignal | undefined] {
  if (optionsOrSignal && "aborted" in optionsOrSignal) return [undefined, optionsOrSignal];
  return [optionsOrSignal as T | undefined, signal];
}

function authFieldsPath(path: string, query?: Pick<Query, "fields">): string {
  return query?.fields === undefined ? path : `${path}?fields=${encodeURIComponent(query.fields)}`;
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


function filterLiteral(value: unknown): string {
  switch (typeof value) {
    case "boolean":
    case "number":
      // This intentionally preserves PocketBase's `NaN` and `Infinity`
      // spelling. The server decides whether a particular field accepts it.
      return String(value);
    case "string":
      return JSON.stringify(value);
    case "undefined":
      return "null";
    default:
      if (value === null) return "null";
      if (value instanceof Date) return JSON.stringify(value.toISOString().replace("T", " "));
      const serialized = JSON.stringify(value);
      // PocketBase deliberately encodes aggregate JSON as one quoted filter
      // string rather than treating it as filter syntax.
      if (serialized?.startsWith("[") || serialized?.startsWith("{")) return JSON.stringify(serialized);
      // JSON.stringify(function () {}) and JSON.stringify(Symbol()) are
      // undefined. Returning that value lets String.replace retain the
      // upstream SDK's observable `undefined` replacement behavior.
      return serialized as unknown as string;
  }
}
