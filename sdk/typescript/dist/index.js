export * from "./types.js";
export * from "./auth-store.js";
export { RealtimeService } from "./realtime.js";
import { AuthStore } from "./auth-store.js";
import { RealtimeService } from "./realtime.js";
import { PgBaseError, MFAChallengeError } from "./types.js";
import { parseEventStream, canonicalDeliveryCursor, deliveryCursor, recordEvent, waitForReconnect } from "./sse.js";
export * from "./admin.js";
import { responseJSON } from "./response.js";
export { browserAuthStorage } from "./state-storage.js";
export class PgBaseClient {
    baseURL;
    auth;
    realtime;
    fetcher;
    pendingRefresh;
    pendingLogout;
    refreshGeneration;
    refreshOrigin;
    requestControllers = new Map();
    /** Cancel older reads with the same request URL, or requests with an explicit key. */
    autoCancellation = true;
    constructor(baseURL, options = {}) {
        this.baseURL = baseURL;
        this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
        this.auth = options.auth ?? new AuthStore();
        this.realtime = new RealtimeService(this);
    }
    register(email, password, options, signal) {
        return this.authenticate("/api/v1/auth/register", email, password, options, signal);
    }
    /** Binds authentication calls to an explicit auth realm without changing record APIs. */
    realm(name) {
        return new AuthRealm(this, name);
    }
    login(email, password, options, signal) {
        return this.authenticate("/api/v1/auth/login", email, password, options, signal).catch((error) => {
            if (error instanceof PgBaseError && error.code === "mfa_required") {
                const [challengeToken] = error.details.challengeToken ?? [];
                const [expiresAt] = error.details.expiresAt ?? [];
                if (challengeToken && expiresAt)
                    throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
            }
            throw error;
        });
    }
    session(options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.request(authFieldsPath("/api/v1/auth/session", query), {}, requestSignal, query?.requestKey);
    }
    logout(signal) { return this._logout("/api/v1/auth/logout", signal); }
    /** Internal session rotation/logout ordering shared by public and realm auth. */
    _refresh(path, signal) {
        const queuedGeneration = this.auth.generation;
        const queuedOrigin = this.refreshGeneration === queuedGeneration ? this.refreshOrigin : queuedGeneration;
        const previous = this.pendingRefresh;
        const logout = this.pendingLogout;
        const operation = (async () => {
            if (logout)
                await logout.catch(() => { });
            if (previous)
                await previous.catch(() => { });
            // Earlier queued rotations may replace our token, but an explicit login
            // or logout must not let old work act on another session.
            if (queuedGeneration !== this.auth.generation && (this.refreshGeneration !== this.auth.generation || this.refreshOrigin !== queuedOrigin)) {
                throw new PgBaseError(403, "session_changed");
            }
            if (!this.auth.token)
                throw new PgBaseError(0, "authentication_required");
            const generation = this.auth.generation;
            const session = refreshSession(await this._request(path, { method: "POST" }, signal));
            assertAuthGeneration(this.auth, generation);
            if (this.refreshGeneration !== generation)
                this.refreshOrigin = generation;
            this.refreshGeneration = generation + 1;
            return acceptAuthSession(this.auth, generation, session, true);
        })();
        this.pendingRefresh = operation;
        const clear = () => { if (this.pendingRefresh === operation)
            this.pendingRefresh = undefined; };
        void operation.then(clear, clear);
        return operation;
    }
    _logout(path, signal) {
        const generation = this.auth.generation;
        const refresh = this.pendingRefresh;
        const origin = this.refreshGeneration === generation ? this.refreshOrigin : generation;
        const operation = (async () => {
            if (refresh)
                await refresh.catch(() => { });
            if (generation !== this.auth.generation && (this.refreshGeneration !== this.auth.generation || this.refreshOrigin !== origin))
                return;
            const current = this.auth.generation;
            try {
                await this._request(path, { method: "POST" }, signal);
            }
            finally {
                if (current === this.auth.generation)
                    this.auth.clear();
            }
        })();
        this.pendingLogout = operation;
        const clear = () => { if (this.pendingLogout === operation)
            this.pendingLogout = undefined; };
        void operation.then(clear, clear);
        return operation;
    }
    async changePassword(currentPassword, newPassword, signal) {
        const generation = this.auth.generation;
        try {
            await this.request("/api/v1/auth/change-password", {
                method: "POST",
                body: JSON.stringify({ currentPassword, newPassword }),
            }, signal);
        }
        finally {
            if (generation === this.auth.generation)
                this.auth.clear();
        }
    }
    requestPasswordReset(email, signal) {
        return this.request("/api/v1/auth/request-password-reset", {
            method: "POST",
            body: JSON.stringify({ email }),
        }, signal);
    }
    requestOTP(email, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.request(authFieldsPath("/api/v1/auth/otp/request", query), { method: "POST", body: JSON.stringify({ email }) }, requestSignal, query?.requestKey);
    }
    async completeOTP(otpId, code, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const generation = this.auth.generation;
        try {
            const session = await this.request(authFieldsPath("/api/v1/auth/otp/complete", query), { method: "POST", body: JSON.stringify({ otpId, code }) }, requestSignal, query?.requestKey);
            return acceptAuthSession(this.auth, generation, session, !!(session.token && session.user));
        }
        catch (error) {
            if (error instanceof PgBaseError && error.code === "mfa_required") {
                const [challengeToken] = error.details.challengeToken ?? [];
                const [expiresAt] = error.details.expiresAt ?? [];
                if (challengeToken && expiresAt)
                    throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
            }
            throw error;
        }
    }
    confirmPasswordReset(token, newPassword, signal) {
        return this.request("/api/v1/auth/confirm-password-reset", {
            method: "POST",
            body: JSON.stringify({ token, newPassword }),
        }, signal);
    }
    requestVerification(signal) {
        return this.request("/api/v1/auth/request-verification", { method: "POST", body: "{}" }, signal);
    }
    confirmVerification(token, signal) {
        return this.request("/api/v1/auth/confirm-verification", {
            method: "POST",
            body: JSON.stringify({ token }),
        }, signal);
    }
    requestEmailChange(currentPassword, newEmail, signal) {
        return this.request("/api/v1/auth/request-email-change", {
            method: "POST",
            body: JSON.stringify({ currentPassword, newEmail }),
        }, signal);
    }
    async confirmEmailChange(token, signal) {
        const generation = this.auth.generation;
        try {
            await this.request("/api/v1/auth/confirm-email-change", {
                method: "POST",
                body: JSON.stringify({ token }),
            }, signal);
        }
        finally {
            if (generation === this.auth.generation)
                this.auth.clear();
        }
    }
    records(collection) {
        return new Records(this, collection);
    }
    /**
     * Constructs a pgBase filter expression without interpolating values.
     *
     * Parameters use `{:name}` placeholders.  Strings, dates,
     * arrays, and objects are emitted as quoted filter literals; scalar boolean,
     * number, and null values retain their filter-literal form.  A placeholder
     * without a matching parameter is left unchanged so server validation can
     * report the incomplete expression.
     */
    filter(expression, params) {
        if (!params || Object.keys(params).length === 0)
            return expression;
        return expression.replace(/\{:([\w.-]+)\}/g, (placeholder, key) => Object.prototype.hasOwnProperty.call(params, key) ? filterLiteral(params[key]) : placeholder);
    }
    batch(operations, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ operations }) }, requestSignal, query?.requestKey);
    }
    batchRequests(requests, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ requests }) }, requestSignal, query?.requestKey);
    }
    batchMultipart(requests, files, signal) {
        const body = new FormData();
        body.append("requests", JSON.stringify({ requests }));
        for (const [key, file] of Object.entries(files)) {
            if (key === "requests" || key === "" || file.filename === "")
                throw new PgBaseError(0, "invalid_batch_file");
            body.append(key, file.blob, file.filename);
        }
        return this.request("/api/v1/user/collections/batch", { method: "POST", body }, signal);
    }
    files(collection, recordID) {
        return new Files(this, collection, recordID);
    }
    async completeMFA(challengeToken, code, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const generation = this.auth.generation;
        const session = await this.request(authFieldsPath("/api/v1/auth/mfa/complete", query), { method: "POST", body: JSON.stringify({ challengeToken, code }) }, requestSignal, query?.requestKey);
        return acceptAuthSession(this.auth, generation, session, !!(session.token && session.user));
    }
    beginTOTPEnrollment(currentPassword, signal) {
        return this.request("/api/v1/auth/totp/enroll", { method: "POST", body: JSON.stringify({ currentPassword }) }, signal);
    }
    confirmTOTPEnrollment(code, signal) {
        return this.request("/api/v1/auth/totp/confirm", { method: "POST", body: JSON.stringify({ code }) }, signal);
    }
    disableTOTP(currentPassword, code, signal) {
        return this.request("/api/v1/auth/totp/disable", {
            method: "POST",
            body: JSON.stringify({ currentPassword, code }),
        }, signal);
    }
    /**
     * Reads every matching record, one cursor page at a time. Pages default to
     * the server's 200-row maximum; `query.limit` overrides it. The server still
     * shortens a page that would exceed its response budget.
     */
    async *iterate(collection, query = {}, signal) {
        // Buffered rows and the next cursor belong to the session that began reading.
        const generation = this.auth.generation;
        let cursor = query.cursor;
        do {
            assertAuthGeneration(this.auth, generation);
            const page = await this.records(collection).list({ ...query, cursor, limit: query.limit ?? iteratePageSize, total: undefined }, signal);
            for (const record of page.items) {
                assertAuthGeneration(this.auth, generation);
                yield record;
            }
            cursor = page.nextCursor;
        } while (cursor);
    }
    _request(path, init, signal, requestKey) {
        return this.request(path, init, signal, requestKey);
    }
    /** Cancels an in-flight request started with this key. */
    cancelRequest(requestKey) { this.requestControllers.get(requestKey)?.abort(); }
    /** Cancels every keyed in-flight request. */
    cancelAllRequests() { for (const controller of this.requestControllers.values())
        controller.abort(); this.requestControllers.clear(); }
    /** Low-level request; null disables cancellation and a string supplies an explicit key. */
    send(path, init = {}, options = {}) {
        return inAuthScope(this.auth, async () => (await this.readResponse(path, init, options.signal, options.requestKey, (response) => responseJSON(response, () => { throw new PgBaseError(response.status, "invalid_response_body"); }))).body);
    }
    async _response(path, init = {}, signal, requestKey) {
        return this.readResponse(path, init, signal, requestKey, async (response) => await responseJSON(response, () => { throw new PgBaseError(response.status, "invalid_response_body"); }));
    }
    async _responseText(path, init = {}, signal, requestKey) {
        return this.readResponse(path, init, signal, requestKey, (response) => response.text());
    }
    async readResponse(path, init, signal, requestKey, read) {
        const generation = this.auth.generation;
        const method = (init.method ?? "GET").toUpperCase();
        const key = requestKey === undefined && (method === "GET" || method === "HEAD") ? `${method} ${path}` : requestKey;
        // RequestInit.signal is part of send's public fetch-compatible contract.
        // Both sources stay active when the caller also supplies RequestOptions.
        const keyed = this.keyedSignal(key, signal, init.signal);
        try {
            assertAuthGeneration(this.auth, generation);
            const response = await this.fetchResponse(path, init, keyed.signal);
            assertAuthGeneration(this.auth, generation);
            const body = await read(response);
            assertAuthGeneration(this.auth, generation);
            const result = { body, etag: response.headers.get("ETag") ?? undefined };
            if (response.headers.get("PgBase-Enrichment") === "failed")
                result.enrichmentFailed = true;
            return result;
        }
        catch (error) {
            assertAuthGeneration(this.auth, generation);
            throw error;
        }
        finally {
            keyed.cleanup();
        }
    }
    async _eventStream(path, lastEventId, signal) {
        const headers = new Headers({ Accept: "text/event-stream" });
        if (this.auth.token) {
            headers.set("Authorization", `Bearer ${this.auth.token}`);
        }
        if (lastEventId !== undefined) {
            headers.set("Last-Event-ID", String(lastEventId));
        }
        const response = await this.fetcher(new URL(path, this.baseURL), { headers, signal });
        if (!response.ok) {
            const body = await responseJSON(response, () => undefined);
            const error = body?.error;
            throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined, response.headers.get("Retry-After") ?? undefined);
        }
        if (!response.body) {
            throw new PgBaseError(0, "event_stream_unavailable");
        }
        return response;
    }
    async authenticate(path, email, password, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const generation = this.auth.generation;
        const session = await this.request(`${path}${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, {
            method: "POST",
            body: JSON.stringify({ email, password, ...(path.endsWith("/register") && query?.profile !== undefined ? { fields: query.profile } : {}) }),
        }, requestSignal, query?.requestKey);
        return acceptAuthSession(this.auth, generation, session, !!session.token);
    }
    request(path, init = {}, signal, requestKey) {
        return inAuthScope(this.auth, async () => (await this._response(path, init, signal, requestKey)).body);
    }
    async fetchResponse(path, init = {}, signal) {
        const base = new URL(this.baseURL);
        const url = new URL(path, base);
        if (url.origin !== base.origin)
            throw new PgBaseError(0, "invalid_path");
        const headers = new Headers(init.headers);
        headers.set("Accept", "application/json");
        if (this.auth.token) {
            headers.set("Authorization", `Bearer ${this.auth.token}`);
        }
        if (init.body !== undefined && !(init.body instanceof FormData)) {
            headers.set("Content-Type", "application/json");
        }
        const response = await this.fetcher(url, { ...init, headers, signal });
        if (!response.ok) {
            const body = await responseJSON(response, () => undefined);
            const error = body?.error;
            throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined, response.headers.get("Retry-After") ?? undefined);
        }
        return response;
    }
    keyedSignal(key, ...signals) {
        const sources = [...new Set(signals.filter((signal) => !!signal))];
        const requestKey = key && this.autoCancellation ? key : undefined;
        if (!requestKey && sources.length < 2)
            return { signal: sources[0], cleanup: () => { } };
        if (requestKey)
            this.cancelRequest(requestKey);
        const controller = new AbortController();
        if (requestKey)
            this.requestControllers.set(requestKey, controller);
        const removeListeners = [];
        for (const signal of sources) {
            const abort = () => controller.abort(signal.reason);
            if (signal.aborted) {
                abort();
                break;
            }
            signal.addEventListener("abort", abort, { once: true });
            removeListeners.push(() => signal.removeEventListener("abort", abort));
        }
        return { signal: controller.signal, cleanup: () => {
                for (const remove of removeListeners)
                    remove();
                if (requestKey && this.requestControllers.get(requestKey) === controller)
                    this.requestControllers.delete(requestKey);
            } };
    }
}
/** The server's maximum native page size; larger values are clamped to it. */
const iteratePageSize = 200;
function assertAuthGeneration(auth, generation) {
    if (generation !== auth.generation)
        throw new PgBaseError(403, "session_changed");
}
/** Check the final auth continuation and account for exactly our own save. */
function acceptAuthSession(auth, generation, session, adopt) {
    assertAuthGeneration(auth, generation);
    if (adopt) {
        const expected = generation + 1;
        try {
            auth.save(session.token, session.user);
        }
        catch (error) {
            assertAuthGeneration(auth, expected);
            throw error;
        }
        // Storage/listeners are caller-owned and may synchronously change auth.
        assertAuthGeneration(auth, expected);
    }
    return session;
}
/** Includes a wrapper's asynchronous mapping, not only its HTTP body read. */
async function inAuthScope(auth, action) {
    const generation = auth.generation;
    try {
        const result = await action();
        assertAuthGeneration(auth, generation);
        return result;
    }
    catch (error) {
        assertAuthGeneration(auth, generation);
        throw error;
    }
}
export class AuthRealm {
    client;
    name;
    constructor(client, name) {
        this.client = client;
        this.name = name;
    }
    path(action) { return `/api/v1/auth/${encodeURIComponent(this.name)}/${action}`; }
    async authenticate(action, email, password, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const generation = this.client.auth.generation;
        try {
            const session = await this.client._request(`${this.path(action)}${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ email, password, ...(action === "register" && query?.profile !== undefined ? { fields: query.profile } : {}) }) }, requestSignal, query?.requestKey);
            return acceptAuthSession(this.client.auth, generation, session, !!session.token);
        }
        catch (error) {
            if (error instanceof PgBaseError && error.code === "mfa_required") {
                const [challengeToken] = error.details.challengeToken ?? [];
                const [expiresAt] = error.details.expiresAt ?? [];
                if (challengeToken && expiresAt)
                    throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
            }
            throw error;
        }
    }
    register(email, password, options, signal) { return this.authenticate("register", email, password, options, signal); }
    login(email, password, options, signal) { return this.authenticate("login", email, password, options, signal); }
    /** Returns the sign-in methods enabled for this auth collection. */
    methods(signal) {
        return inAuthScope(this.client.auth, async () => {
            const methods = await this.client._request(this.path("methods"), {}, signal);
            return { ...methods, oauth2: methods.oauth2 ?? { enabled: false, providers: [] } };
        });
    }
    /** Rotates the current bearer token after validating its replacement session. */
    refresh(signal) { return this.client._refresh(this.path("refresh"), signal); }
    session(signal) { return this.client._request(this.path("session"), {}, signal); }
    logout(signal) { return this.client._logout(this.path("logout"), signal); }
    requestPasswordReset(email, signal) { return this.client._request(this.path("request-password-reset"), { method: "POST", body: JSON.stringify({ email }) }, signal); }
    requestOTP(email, signal) { return this.client._request(this.path("otp/request"), { method: "POST", body: JSON.stringify({ email }) }, signal); }
    async completeOTP(otpId, code, signal) {
        const generation = this.client.auth.generation;
        try {
            const session = await this.client._request(this.path("otp/complete"), { method: "POST", body: JSON.stringify({ otpId, code }) }, signal);
            return acceptAuthSession(this.client.auth, generation, session, !!session.token);
        }
        catch (error) {
            if (error instanceof PgBaseError && error.code === "mfa_required") {
                const [challengeToken] = error.details.challengeToken ?? [];
                const [expiresAt] = error.details.expiresAt ?? [];
                if (challengeToken && expiresAt)
                    throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
            }
            throw error;
        }
    }
    profile(options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const parameters = new URLSearchParams();
        if (query?.expand !== undefined)
            parameters.set("expand", query.expand);
        if (query?.fields !== undefined)
            parameters.set("fields", query.fields);
        return inAuthScope(this.client.auth, async () => (await this.client._request(`${this.path("profile")}${parameters.size ? `?${parameters}` : ""}`, {}, requestSignal, query?.requestKey)).attributes);
    }
    updateProfile(profile, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const suffix = query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`;
        return inAuthScope(this.client.auth, async () => (await this.client._request(`${this.path("profile")}${suffix}`, { method: "PATCH", body: JSON.stringify(profile) }, requestSignal, query?.requestKey)).attributes);
    }
    oauthStart(provider, redirectUri, options, signal) { const [start, requestSignal] = oauthStartArguments(options, signal); return this.client._request(this.path("oauth/start"), { method: "POST", body: JSON.stringify({ provider, redirectUri, ...start }) }, requestSignal); }
    /** Starts OAuth and delegates browser/popup/deep-link navigation to the host. */
    async oauthStartAndOpen(provider, redirectUri, options, signal) {
        const started = await this.oauthStart(provider, redirectUri, { requireCodeVerifier: options.requireCodeVerifier }, signal);
        await options.open(started.authorizationUrl);
        return started;
    }
    async oauthWithBrowser(provider, redirectUri, adapter, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        try {
            // The verifier never leaves this call, so a leaked callback URL cannot complete the flow.
            const started = await this.oauthStart(provider, redirectUri, { requireCodeVerifier: true }, requestSignal);
            const expectedState = new URL(started.authorizationUrl).searchParams.get("state");
            if (!expectedState)
                throw new OAuthCallbackError("invalid_start", "The OAuth start URL is missing state.");
            await adapter.open(started.authorizationUrl);
            const callback = await callbackWithAbort(adapter, requestSignal);
            if (callback.origin !== new URL(redirectUri).origin)
                throw new OAuthCallbackError("invalid_callback_origin", "The OAuth callback origin does not match the redirect URL.");
            const providerError = callback.searchParams.get("error");
            if (providerError)
                throw new OAuthCallbackError(providerError, callback.searchParams.get("error_description") ?? undefined);
            const state = callback.searchParams.get("state");
            const code = callback.searchParams.get("code");
            if (!state || !code)
                throw new OAuthCallbackError("invalid_callback", "The OAuth callback is missing state or code.");
            if (state !== expectedState)
                throw new OAuthCallbackError("invalid_state", "The OAuth callback state does not match the OAuth start state.");
            return await this.oauthComplete(provider, state, code, { ...query, codeVerifier: started.codeVerifier }, requestSignal);
        }
        finally {
            adapter.close?.();
        }
    }
    oauthLink(currentPassword, provider, redirectUri, options, signal) { const [start, requestSignal] = oauthStartArguments(options, signal); return this.client._request("/api/v1/auth/oauth/link", { method: "POST", body: JSON.stringify({ currentPassword, provider, redirectUri, ...start }) }, requestSignal); }
    async oauthComplete(provider, state, code, options, signal) { const [query, requestSignal] = recordGetArguments(options, signal); const generation = this.client.auth.generation; const session = await this.client._request(authFieldsPath("/api/v1/auth/oauth/complete", query), { method: "POST", body: JSON.stringify({ provider, state, code, ...(query?.codeVerifier === undefined ? {} : { codeVerifier: query.codeVerifier }), ...(query?.profile === undefined ? {} : { fields: query.profile }) }) }, requestSignal, query?.requestKey); return acceptAuthSession(this.client.auth, generation, session, !!(session.token && session.user)); }
    oauthCompleteFromURL(provider, callbackURL, options, signal) {
        const url = typeof callbackURL === "string" ? new URL(callbackURL) : callbackURL;
        const providerError = url.searchParams.get("error");
        if (providerError)
            throw new OAuthCallbackError(providerError, url.searchParams.get("error_description") ?? undefined);
        const state = url.searchParams.get("state");
        const code = url.searchParams.get("code");
        if (!state || !code)
            throw new OAuthCallbackError("invalid_callback", "The OAuth callback is missing state or code.");
        return this.oauthComplete(provider, state, code, options, signal);
    }
}
/** Refresh responses always contain a replacement token, unlike session reads. */
function refreshSession(value) {
    if (!value || typeof value !== "object")
        throw new PgBaseError(0, "invalid_session_response");
    const session = value;
    const user = session.user;
    if (typeof session.token !== "string" || session.token.length === 0 || typeof session.expiresAt !== "string" || !user || typeof user !== "object") {
        throw new PgBaseError(0, "invalid_session_response");
    }
    const details = user;
    if (typeof details.id !== "string" || typeof details.email !== "string" || typeof details.createdAt !== "string") {
        throw new PgBaseError(0, "invalid_session_response");
    }
    if (details.verified !== undefined && typeof details.verified !== "boolean")
        throw new PgBaseError(0, "invalid_session_response");
    if (details.authCollection !== undefined && typeof details.authCollection !== "string")
        throw new PgBaseError(0, "invalid_session_response");
    if (details.authCollectionId !== undefined && typeof details.authCollectionId !== "string")
        throw new PgBaseError(0, "invalid_session_response");
    return { token: session.token, expiresAt: session.expiresAt, user: details };
}
async function callbackWithAbort(adapter, signal) {
    if (signal?.aborted)
        throw new DOMException("aborted", "AbortError");
    const callback = adapter.waitForCallback(signal).then((value) => typeof value === "string" ? new URL(value) : value);
    if (!signal)
        return callback;
    let abort;
    try {
        return await Promise.race([callback, new Promise((_, reject) => {
                abort = () => reject(new DOMException("aborted", "AbortError"));
                signal.addEventListener("abort", abort, { once: true });
                if (signal.aborted)
                    abort();
            })]);
    }
    finally {
        signal.removeEventListener("abort", abort);
    }
}
export class OAuthCallbackError extends Error {
    code;
    constructor(code, message) {
        super(message ?? code);
        this.code = code;
        this.name = "OAuthCallbackError";
    }
}
export class Records {
    client;
    collection;
    constructor(client, collection) {
        this.client = client;
        this.collection = collection;
    }
    list(query = {}, signal) {
        return this.client._request(this.listPath(query), {}, signal, query.requestKey);
    }
    /** Returns the complete { items, nextCursor? } JSON envelope without parsing numbers. */
    listJSON(query = {}, signal) {
        return inAuthScope(this.client.auth, async () => (await this.client._responseText(this.listPath(query), {}, signal, query.requestKey)).body);
    }
    /**
     * Lists a numbered PocketBase-compatible page. This is intentionally
     * separate from [list], whose cursor contract remains native PgBase.
     */
    getList(page = 1, perPage = 30, options = {}, signal) {
        const parameters = new URLSearchParams({ compat: "pocketbase", page: String(page), perPage: String(perPage) });
        for (const [key, value] of Object.entries(options))
            if (key !== "requestKey" && value !== undefined)
                parameters.set(key, String(value));
        return this.client._request(`${this.path()}?${parameters}`, {}, signal, options.requestKey);
    }
    get(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return inAuthScope(this.client.auth, async () => (await this.getWithETag(id, options, requestSignal)).record);
    }
    getWithETag(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return this.recordResponse(this.path(id, options), {}, requestSignal, options?.requestKey);
    }
    getJSON(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return this.recordJSONResponse(this.path(id, options), {}, requestSignal, options?.requestKey);
    }
    /** Returns the response body. Use createWithETag to detect an ID-only committed write. */
    create(data, idempotencyKey, options, signal) {
        return inAuthScope(this.client.auth, async () => (await this.createWithETag(data, idempotencyKey, options, signal)).record);
    }
    createWithETag(data, idempotencyKey, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.mutationResponse(this.path(undefined, query), {
            method: "POST",
            headers: { "Idempotency-Key": idempotencyKey },
            body: JSON.stringify(data),
        }, requestSignal, query?.requestKey);
    }
    /** Sends a JSON object verbatim; retry an uncertain create with the same body and key. */
    createJSON(recordJSON, idempotencyKey, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.recordJSONResponse(this.path(undefined, query), {
            method: "POST",
            headers: { "Idempotency-Key": idempotencyKey },
            body: recordJSON,
        }, requestSignal, query?.requestKey);
    }
    /** Returns the response body. Use updateWithETag to detect an ID-only committed write. */
    update(id, data, etag, options, signal) {
        return inAuthScope(this.client.auth, async () => (await this.updateWithETag(id, data, etag, options, signal)).record);
    }
    updateWithETag(id, data, etag, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.mutationResponse(this.path(id, query), {
            method: "PATCH",
            headers: { "If-Match": etag },
            body: JSON.stringify(data),
        }, requestSignal, query?.requestKey);
    }
    /** Sends a record update JSON object verbatim with the current record ETag. */
    updateJSON(id, recordJSON, etag, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.recordJSONResponse(this.path(id, query), {
            method: "PATCH",
            headers: { "If-Match": etag },
            body: recordJSON,
        }, requestSignal, query?.requestKey);
    }
    remove(id, etag, signal) {
        return this.client._request(this.path(id), {
            method: "DELETE",
            headers: { "If-Match": etag },
        }, signal);
    }
    async *events(options = {}) {
        if (options.fields)
            throw new PgBaseError(0, "unsupported_event_projection", { fields: ["Metadata events do not contain records; use realtime.subscribe for projected record payloads."] });
        if (options.maxReconnects !== undefined && (!Number.isInteger(options.maxReconnects) || options.maxReconnects < 0))
            throw new PgBaseError(0, "invalid_reconnect_options");
        if (options.reconnectDelayMs !== undefined && (!Number.isFinite(options.reconnectDelayMs) || options.reconnectDelayMs < 0))
            throw new PgBaseError(0, "invalid_reconnect_options");
        let lastEventId = options.lastEventId === undefined ? undefined : canonicalDeliveryCursor(options.lastEventId);
        const generation = this.client.auth.generation;
        const controller = new AbortController();
        const stopOnAuthChange = this.client.auth._onChange((nextGeneration) => {
            if (nextGeneration !== generation) {
                controller.abort();
            }
        });
        const abort = () => controller.abort();
        if (options.signal?.aborted)
            abort();
        else
            options.signal?.addEventListener("abort", abort, { once: true });
        const maximumReconnects = options.maxReconnects ?? 5;
        const initialDelay = options.reconnectDelayMs ?? 250;
        let reconnects = 0;
        const deliveredEventIDs = new Set();
        try {
            while (!controller.signal.aborted && generation === this.client.auth.generation) {
                try {
                    const response = await this.client._eventStream(this.eventsPath(), lastEventId, controller.signal);
                    for await (const message of parseEventStream(response.body, controller.signal)) {
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
                            deliveredEventIDs.delete(deliveredEventIDs.values().next().value);
                        }
                        reconnects = 0;
                        yield event;
                    }
                    throw new PgBaseError(0, "realtime_stream_ended");
                }
                catch (error) {
                    if (controller.signal.aborted || generation !== this.client.auth.generation) {
                        return;
                    }
                    if (error instanceof PgBaseError && ([400, 401, 403, 410].includes(error.status) || error.code === "cursor_expired"))
                        throw error;
                    if (reconnects >= maximumReconnects) {
                        throw error;
                    }
                }
                reconnects += 1;
                const delay = Math.min(initialDelay * 2 ** (reconnects - 1), 10_000);
                await waitForReconnect(delay, controller.signal);
            }
        }
        finally {
            controller.abort();
            options.signal?.removeEventListener("abort", abort);
            stopOnAuthChange();
        }
    }
    listPath(query) {
        const parameters = new URLSearchParams();
        for (const [key, value] of Object.entries(query)) {
            if (key !== "requestKey" && value !== undefined)
                parameters.set(key, String(value));
        }
        return `${this.path()}${parameters.size ? `?${parameters}` : ""}`;
    }
    path(id, query) {
        const base = `/api/v1/user/collections/${encodeURIComponent(this.collection)}/records`;
        if (id === undefined) {
            const parameters = new URLSearchParams();
            if (query?.fields !== undefined)
                parameters.set("fields", query.fields);
            return parameters.size ? `${base}?${parameters}` : base;
        }
        const parameters = new URLSearchParams();
        if (query?.expand !== undefined)
            parameters.set("expand", query.expand);
        if (query?.fields !== undefined)
            parameters.set("fields", query.fields);
        const suffix = parameters.size ? `?${parameters}` : "";
        return `${base}/${encodeURIComponent(id)}${suffix}`;
    }
    eventsPath() {
        return `/api/v1/user/collections/${encodeURIComponent(this.collection)}/events`;
    }
    recordResponse(path, init, signal, requestKey) {
        return inAuthScope(this.client.auth, async () => {
            const response = await this.client._response(path, init, signal, requestKey);
            return { record: response.body, etag: response.etag };
        });
    }
    mutationResponse(path, init, signal, requestKey) {
        return inAuthScope(this.client.auth, async () => {
            const { body, etag, enrichmentFailed } = await this.client._response(path, init, signal, requestKey);
            return enrichmentFailed
                ? { record: body, etag, enrichmentFailed }
                : { record: body, etag };
        });
    }
    recordJSONResponse(path, init, signal, requestKey) {
        return inAuthScope(this.client.auth, async () => {
            const response = await this.client._responseText(path, init, signal, requestKey);
            const result = { recordJSON: response.body, etag: response.etag };
            if (response.enrichmentFailed)
                result.enrichmentFailed = true;
            return result;
        });
    }
}
function recordGetArguments(optionsOrSignal, signal) {
    if (optionsOrSignal && "aborted" in optionsOrSignal)
        return [undefined, optionsOrSignal];
    return [optionsOrSignal, signal];
}
function oauthStartArguments(optionsOrSignal, signal) {
    if (optionsOrSignal && "aborted" in optionsOrSignal)
        return [{}, optionsOrSignal];
    // Servers before the opt-in reject unknown keys, so the key is sent only when requested.
    return [optionsOrSignal?.requireCodeVerifier ? { requireCodeVerifier: true } : {}, signal];
}
function authFieldsPath(path, query) {
    return query?.fields === undefined ? path : `${path}?fields=${encodeURIComponent(query.fields)}`;
}
export class Files {
    client;
    collection;
    recordID;
    constructor(client, collection, recordID) {
        this.client = client;
        this.collection = collection;
        this.recordID = recordID;
    }
    list(field, signal) {
        return this.client._request(this.fieldPath(field), {}, signal);
    }
    async upload(field, file, filename, signal) {
        const generation = this.client.auth.generation;
        try {
            const bytes = await file.arrayBuffer();
            assertAuthGeneration(this.client.auth, generation);
            const digest = await crypto.subtle.digest("SHA-256", bytes);
            assertAuthGeneration(this.client.auth, generation);
            const body = new FormData();
            body.append("file", file, filename);
            const result = await this.client._request(this.fieldPath(field), {
                method: "POST",
                headers: {
                    "X-PgBase-File-Size": String(file.size),
                    "X-PgBase-File-SHA256": hex(new Uint8Array(digest)),
                },
                body,
            }, signal);
            assertAuthGeneration(this.client.auth, generation);
            return result;
        }
        catch (error) {
            assertAuthGeneration(this.client.auth, generation);
            throw error;
        }
    }
    delete(fileID, signal) {
        return this.client._request(this.filePath(fileID), { method: "DELETE" }, signal);
    }
    mintDownloadToken(fileID, variant = "original", signal) {
        return this.client._request(`${this.filePath(fileID)}/token`, { method: "POST", body: JSON.stringify({ variant }) }, signal);
    }
    downloadURL(fileID, token, variant = "original") {
        const url = new URL(this.filePath(fileID), this.client.baseURL);
        url.searchParams.set("token", token);
        if (variant !== "original")
            url.searchParams.set("variant", variant);
        return url.toString();
    }
    fieldPath(field) {
        return `/api/v1/user/collections/${encodeURIComponent(this.collection)}/records/${encodeURIComponent(this.recordID)}/files/${encodeURIComponent(field)}`;
    }
    filePath(fileID) {
        return `/api/v1/user/files/${encodeURIComponent(fileID)}`;
    }
}
function hex(bytes) {
    return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}
function filterLiteral(value) {
    switch (typeof value) {
        case "boolean":
        case "number":
            // Preserve non-finite numeric tokens for server validation.
            return String(value);
        case "string":
            return JSON.stringify(value);
        case "undefined":
            return "null";
        default:
            if (value === null)
                return "null";
            if (value instanceof Date)
                return JSON.stringify(value.toISOString().replace("T", " "));
            const serialized = JSON.stringify(value);
            // Aggregate JSON is encoded as one quoted filter
            // string rather than treating it as filter syntax.
            if (serialized?.startsWith("[") || serialized?.startsWith("{"))
                return JSON.stringify(serialized);
            // JSON.stringify(function () {}) and JSON.stringify(Symbol()) are
            // undefined. Returning that value lets String.replace retain the
            // upstream SDK's observable `undefined` replacement behavior.
            return serialized;
    }
}
