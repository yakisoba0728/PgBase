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
    async logout(signal) {
        try {
            await this.request("/api/v1/auth/logout", { method: "POST" }, signal);
        }
        finally {
            this.auth.clear();
        }
    }
    async changePassword(currentPassword, newPassword, signal) {
        try {
            await this.request("/api/v1/auth/change-password", {
                method: "POST",
                body: JSON.stringify({ currentPassword, newPassword }),
            }, signal);
        }
        finally {
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
            if (session.token && session.user && generation === this.auth.generation)
                this.auth.save(session.token, session.user);
            return session;
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
        return this.request("/api/v1/auth/request-verification", { method: "POST" }, signal);
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
        try {
            await this.request("/api/v1/auth/confirm-email-change", {
                method: "POST",
                body: JSON.stringify({ token }),
            }, signal);
        }
        finally {
            this.auth.clear();
        }
    }
    records(collection) {
        return new Records(this, collection);
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
        if (session.token && session.user && generation === this.auth.generation)
            this.auth.save(session.token, session.user);
        return session;
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
    async *iterate(collection, query = {}, signal) {
        let cursor = query.cursor;
        do {
            const page = await this.records(collection).list({ ...query, cursor }, signal);
            yield* page.items;
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
    async send(path, init = {}, options = {}) {
        return (await this.readResponse(path, init, options.signal, options.requestKey, (response) => responseJSON(response, () => { throw new PgBaseError(response.status, "invalid_response_body"); }))).body;
    }
    async _response(path, init = {}, signal, requestKey) {
        return this.readResponse(path, init, signal, requestKey, async (response) => await responseJSON(response, () => { throw new PgBaseError(response.status, "invalid_response_body"); }));
    }
    async _responseText(path, init = {}, signal, requestKey) {
        return this.readResponse(path, init, signal, requestKey, (response) => response.text());
    }
    async readResponse(path, init, signal, requestKey, read) {
        const method = (init.method ?? "GET").toUpperCase();
        const key = requestKey === undefined && (method === "GET" || method === "HEAD") ? `${method} ${path}` : requestKey;
        const keyed = this.keyedSignal(key, signal);
        try {
            const response = await this.fetchResponse(path, init, keyed.signal);
            return { body: await read(response), etag: response.headers.get("ETag") ?? undefined };
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
            throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined);
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
        if (session.token && generation === this.auth.generation) {
            this.auth.save(session.token, session.user);
        }
        return session;
    }
    async request(path, init = {}, signal, requestKey) {
        return (await this._response(path, init, signal, requestKey)).body;
    }
    async fetchResponse(path, init = {}, signal) {
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
            const body = await responseJSON(response, () => undefined);
            const error = body?.error;
            throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined);
        }
        return response;
    }
    keyedSignal(key, signal) {
        if (!key || !this.autoCancellation)
            return { signal, cleanup: () => { } };
        if (this.autoCancellation)
            this.cancelRequest(key);
        const controller = new AbortController();
        this.requestControllers.set(key, controller);
        const abort = () => controller.abort(signal?.reason);
        if (signal?.aborted)
            abort();
        else
            signal?.addEventListener("abort", abort, { once: true });
        return { signal: controller.signal, cleanup: () => {
                signal?.removeEventListener("abort", abort);
                if (this.requestControllers.get(key) === controller)
                    this.requestControllers.delete(key);
            } };
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
            if (session.token && generation === this.client.auth.generation)
                this.client.auth.save(session.token, session.user);
            return session;
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
    async methods(signal) {
        const methods = await this.client._request(this.path("methods"), {}, signal);
        return { ...methods, oauth2: methods.oauth2 ?? { enabled: false, providers: [] } };
    }
    /** Rotates the current bearer token after validating its replacement session. */
    async refresh(signal) {
        const generation = this.client.auth.generation;
        const session = refreshSession(await this.client._request(this.path("refresh"), { method: "POST" }, signal));
        if (generation === this.client.auth.generation)
            this.client.auth.save(session.token, session.user);
        return session;
    }
    session(signal) { return this.client._request(this.path("session"), {}, signal); }
    async logout(signal) { try {
        await this.client._request(this.path("logout"), { method: "POST" }, signal);
    }
    finally {
        this.client.auth.clear();
    } }
    requestPasswordReset(email, signal) { return this.client._request(this.path("request-password-reset"), { method: "POST", body: JSON.stringify({ email }) }, signal); }
    requestOTP(email, signal) { return this.client._request(this.path("otp/request"), { method: "POST", body: JSON.stringify({ email }) }, signal); }
    async completeOTP(otpId, code, signal) {
        const generation = this.client.auth.generation;
        try {
            const session = await this.client._request(this.path("otp/complete"), { method: "POST", body: JSON.stringify({ otpId, code }) }, signal);
            if (session.token && generation === this.client.auth.generation)
                this.client.auth.save(session.token, session.user);
            return session;
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
    async profile(options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const parameters = new URLSearchParams();
        if (query?.expand !== undefined)
            parameters.set("expand", query.expand);
        if (query?.fields !== undefined)
            parameters.set("fields", query.fields);
        return (await this.client._request(`${this.path("profile")}${parameters.size ? `?${parameters}` : ""}`, {}, requestSignal, query?.requestKey)).attributes;
    }
    async updateProfile(profile, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const suffix = query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`;
        return (await this.client._request(`${this.path("profile")}${suffix}`, { method: "PATCH", body: JSON.stringify(profile) }, requestSignal, query?.requestKey)).attributes;
    }
    oauthStart(provider, redirectUri, signal) { return this.client._request(this.path("oauth/start"), { method: "POST", body: JSON.stringify({ provider, redirectUri }) }, signal); }
    /** Starts OAuth and delegates browser/popup/deep-link navigation to the host. */
    async oauthStartAndOpen(provider, redirectUri, options, signal) {
        const started = await this.oauthStart(provider, redirectUri, signal);
        await options.open(started.authorizationUrl);
        return started;
    }
    async oauthWithBrowser(provider, redirectUri, adapter, options, signal) {
        const [, requestSignal] = recordGetArguments(options, signal);
        try {
            const started = await this.oauthStart(provider, redirectUri, requestSignal);
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
            return await this.oauthComplete(provider, state, code, options, requestSignal);
        }
        finally {
            adapter.close?.();
        }
    }
    oauthLink(currentPassword, provider, redirectUri, signal) { return this.client._request("/api/v1/auth/oauth/link", { method: "POST", body: JSON.stringify({ currentPassword, provider, redirectUri }) }, signal); }
    async oauthComplete(provider, state, code, options, signal) { const [query, requestSignal] = recordGetArguments(options, signal); const generation = this.client.auth.generation; const session = await this.client._request(authFieldsPath("/api/v1/auth/oauth/complete", query), { method: "POST", body: JSON.stringify({ provider, state, code, ...(query?.profile === undefined ? {} : { fields: query.profile }) }) }, requestSignal, query?.requestKey); if (session.token && session.user && generation === this.client.auth.generation)
        this.client.auth.save(session.token, session.user); return session; }
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
function callbackWithAbort(adapter, signal) {
    const callback = adapter.waitForCallback(signal).then((value) => typeof value === "string" ? new URL(value) : value);
    if (!signal)
        return callback;
    if (signal.aborted)
        return Promise.reject(new DOMException("aborted", "AbortError"));
    return Promise.race([callback, new Promise((_, reject) => signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true }))]);
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
    async listJSON(query = {}, signal) {
        return (await this.client._responseText(this.listPath(query), {}, signal, query.requestKey)).body;
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
    async get(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return (await this.getWithETag(id, options, requestSignal)).record;
    }
    getWithETag(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return this.recordResponse(this.path(id, options), {}, requestSignal, options?.requestKey);
    }
    getJSON(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return this.recordJSONResponse(this.path(id, options), {}, requestSignal, options?.requestKey);
    }
    async create(data, idempotencyKey, options, signal) {
        return (await this.createWithETag(data, idempotencyKey, options, signal)).record;
    }
    createWithETag(data, idempotencyKey, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.recordResponse(this.path(undefined, query), {
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
    async update(id, data, etag, options, signal) {
        return (await this.updateWithETag(id, data, etag, options, signal)).record;
    }
    updateWithETag(id, data, etag, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.recordResponse(this.path(id, query), {
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
        const deliveredEventIDs = new Set();
        try {
            while (!controller.signal.aborted && generation === this.client.auth.generation) {
                try {
                    const response = await this.client._eventStream(this.eventsPath(options.fields), lastEventId, controller.signal);
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
                }
                catch (error) {
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
        }
        finally {
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
        if (!id) {
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
    eventsPath(fields) {
        const base = `/api/v1/user/collections/${encodeURIComponent(this.collection)}/events`;
        return fields === undefined ? base : `${base}?fields=${encodeURIComponent(fields)}`;
    }
    async recordResponse(path, init, signal, requestKey) {
        const response = await this.client._response(path, init, signal, requestKey);
        return { record: response.body, etag: response.etag };
    }
    async recordJSONResponse(path, init, signal, requestKey) {
        const response = await this.client._responseText(path, init, signal, requestKey);
        return { recordJSON: response.body, etag: response.etag };
    }
}
function recordGetArguments(optionsOrSignal, signal) {
    if (optionsOrSignal && "aborted" in optionsOrSignal)
        return [undefined, optionsOrSignal];
    return [optionsOrSignal, signal];
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
            // This intentionally preserves PocketBase's `NaN` and `Infinity`
            // spelling. The server decides whether a particular field accepts it.
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
            // PocketBase deliberately encodes aggregate JSON as one quoted filter
            // string rather than treating it as filter syntax.
            if (serialized?.startsWith("[") || serialized?.startsWith("{"))
                return JSON.stringify(serialized);
            // JSON.stringify(function () {}) and JSON.stringify(Symbol()) are
            // undefined. Returning that value lets String.replace retain the
            // upstream SDK's observable `undefined` replacement behavior.
            return serialized;
    }
}
