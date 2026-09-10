export * from "./admin.js";
export class PgBaseError extends Error {
    status;
    code;
    details;
    etag;
    constructor(status, code, details = {}, etag) {
        super(code);
        this.status = status;
        this.code = code;
        this.details = details;
        this.etag = etag;
        this.name = "PgBaseError";
    }
}
export class MFAChallengeError extends PgBaseError {
    challenge;
    constructor(status, code, details, challenge) {
        super(status, code, details);
        this.challenge = challenge;
        this.name = "MFAChallengeError";
    }
}
export class AuthStore {
    storage;
    #token;
    #record = null;
    #generation = 0;
    #listeners = new Set();
    constructor(storage) {
        this.storage = storage;
        this.#token = storage?.get() ?? null;
    }
    get token() {
        return this.#token;
    }
    get record() { return this.#record; }
    get isValid() { return this.#token !== null && this.#token.length > 0; }
    get generation() {
        return this.#generation;
    }
    save(token, record = null) {
        this.#token = token;
        this.#record = record;
        this.#generation += 1;
        this.storage?.set(token);
        this.notify();
    }
    clear() {
        this.#token = null;
        this.#record = null;
        this.#generation += 1;
        this.storage?.clear();
        this.notify();
    }
    /** Encodes token and cached auth record for a server-rendered response cookie. */
    exportToCookie(options = {}) {
        const key = options.key ?? "pb_auth";
        const path = options.path ?? "/";
        if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(key) || /[;\r\n]/.test(path))
            throw new PgBaseError(0, "invalid_cookie_option");
        const payload = encodeURIComponent(JSON.stringify({ token: this.#token, record: this.#record }));
        return [`${key}=${payload}`, `Path=${path}`, options.httpOnly === false ? "" : "HttpOnly", options.secure ? "Secure" : "", options.sameSite ? `SameSite=${options.sameSite}` : ""].filter(Boolean).join("; ");
    }
    /** Loads a state exported by [exportToCookie]; malformed cookies clear state. */
    loadFromCookie(cookie, key = "pb_auth") {
        const encoded = cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${key}=`))?.slice(key.length + 1);
        if (!encoded)
            return;
        try {
            const value = JSON.parse(decodeURIComponent(encoded));
            if ((value.token !== null && typeof value.token !== "string") || (value.record !== null && (typeof value.record !== "object" || Array.isArray(value.record))))
                throw new Error("invalid auth cookie");
            this.#token = value.token ?? null;
            this.#record = (value.record ?? null);
            this.#generation += 1;
            this.notify();
        }
        catch {
            this.clear();
        }
    }
    _onChange(listener) {
        this.#listeners.add(listener);
        return () => this.#listeners.delete(listener);
    }
    notify() {
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
export function browserTokenStorage(storage, key = "pgbase.user.token") {
    return {
        get: () => storage.getItem(key),
        set: (token) => storage.setItem(key, token),
        clear: () => storage.removeItem(key),
    };
}
export class PgBaseClient {
    baseURL;
    auth;
    realtime;
    fetcher;
    requestControllers = new Map();
    /** Matches PocketBase's opt-out switch for duplicate request cancellation. */
    autoCancellation = true;
    constructor(baseURL, options = {}) {
        this.baseURL = baseURL;
        this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
        this.auth = options.auth ?? new AuthStore();
        this.realtime = new RealtimeService(this);
    }
    register(email, password, signal) {
        return this.authenticate("/api/v1/auth/register", email, password, signal);
    }
    /** Binds authentication calls to an explicit auth realm without changing record APIs. */
    realm(name) {
        return new AuthRealm(this, name);
    }
    login(email, password, signal) {
        return this.authenticate("/api/v1/auth/login", email, password, signal).catch((error) => {
            if (error instanceof PgBaseError && error.code === "mfa_required") {
                const [challengeToken] = error.details.challengeToken ?? [];
                const [expiresAt] = error.details.expiresAt ?? [];
                if (challengeToken && expiresAt)
                    throw new MFAChallengeError(error.status, error.code, error.details, { challengeToken, expiresAt });
            }
            throw error;
        });
    }
    session(signal) {
        return this.request("/api/v1/auth/session", {}, signal);
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
    requestOTP(email, signal) {
        return this.request("/api/v1/auth/otp/request", { method: "POST", body: JSON.stringify({ email }) }, signal);
    }
    async completeOTP(otpId, code, signal) {
        const generation = this.auth.generation;
        try {
            const session = await this.request("/api/v1/auth/otp/complete", { method: "POST", body: JSON.stringify({ otpId, code }) }, signal);
            if (session.token && generation === this.auth.generation)
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
    batch(operations, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ operations }) }, requestSignal);
    }
    batchRequests(requests, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        return this.request(`/api/v1/user/collections/batch${query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`}`, { method: "POST", body: JSON.stringify({ requests }) }, requestSignal);
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
    async completeMFA(challengeToken, code, signal) {
        const generation = this.auth.generation;
        const session = await this.request("/api/v1/auth/mfa/complete", { method: "POST", body: JSON.stringify({ challengeToken, code }) }, signal);
        if (session.token && generation === this.auth.generation)
            this.auth.save(session.token, session.user);
        return session;
    }
    beginTOTPEnrollment(currentPassword, signal) {
        return this.request("/api/v1/auth/totp/enroll", { method: "POST", body: JSON.stringify({ currentPassword }) }, signal);
    }
    confirmTOTPEnrollment(code, signal) {
        return this.request("/api/v1/auth/totp/confirm", { method: "POST", body: JSON.stringify({ code }) }, signal);
    }
    async *iterate(collection, query = {}, signal) {
        let cursor = query.cursor;
        do {
            const page = await this.records(collection).list({ ...query, cursor }, signal);
            yield* page.items;
            cursor = page.nextCursor;
        } while (cursor);
    }
    _request(path, init, signal) {
        return this.request(path, init, signal);
    }
    /** Cancels an in-flight request started with this key. */
    cancelRequest(requestKey) { this.requestControllers.get(requestKey)?.abort(); }
    /** Cancels every keyed in-flight request. */
    cancelAllRequests() { for (const controller of this.requestControllers.values())
        controller.abort(); this.requestControllers.clear(); }
    /** Low-level request with PocketBase-style request-key auto-cancellation. */
    async send(path, init = {}, options = {}) {
        const keyed = this.keyedSignal(options.requestKey, options.signal);
        try {
            return await this.request(path, init, keyed.signal);
        }
        finally {
            keyed.cleanup();
        }
    }
    async _response(path, init = {}, signal) {
        const response = await this.fetchResponse(path, init, signal);
        const body = await response.json().catch(() => undefined);
        return { body, etag: response.headers.get("ETag") ?? undefined };
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
            const body = await response.json().catch(() => undefined);
            const error = body?.error;
            throw new PgBaseError(response.status, error?.code ?? "request_failed", error?.details ?? {}, response.headers.get("ETag") ?? undefined);
        }
        if (!response.body) {
            throw new PgBaseError(0, "event_stream_unavailable");
        }
        return response;
    }
    async authenticate(path, email, password, signal) {
        const generation = this.auth.generation;
        const session = await this.request(path, {
            method: "POST",
            body: JSON.stringify({ email, password }),
        }, signal);
        if (session.token && generation === this.auth.generation) {
            this.auth.save(session.token, session.user);
        }
        return session;
    }
    async request(path, init = {}, signal) {
        const response = await this.fetchResponse(path, init, signal);
        return await response.json().catch(() => undefined);
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
            const body = await response.json().catch(() => undefined);
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
        if (signal?.aborted)
            controller.abort();
        else
            signal?.addEventListener("abort", () => controller.abort(), { once: true });
        return { signal: controller.signal, cleanup: () => { if (this.requestControllers.get(key) === controller)
                this.requestControllers.delete(key); } };
    }
}
/** Multiplexes PocketBase-compatible realtime topics over one durable SSE connection. */
export class RealtimeService {
    client;
    listeners = new Map();
    controller;
    clientId;
    lastEventId;
    running;
    ready;
    resolveReady;
    constructor(client) {
        this.client = client;
    }
    async subscribe(topic, listener) {
        if (!topic)
            throw new PgBaseError(0, "invalid_realtime_topic");
        if (this.controller?.signal.aborted && this.running)
            await this.running;
        let set = this.listeners.get(topic);
        if (!set) {
            set = new Set();
            this.listeners.set(topic, set);
        }
        set.add(listener);
        this.ensureRunning();
        if (this.clientId)
            await this.sync();
        else
            await this.ready;
        return async () => this.unsubscribe(topic, listener);
    }
    async unsubscribe(topic, listener) {
        if (topic === undefined)
            this.listeners.clear();
        else if (listener === undefined)
            this.listeners.delete(topic);
        else {
            const set = this.listeners.get(topic);
            set?.delete(listener);
            if (set?.size === 0)
                this.listeners.delete(topic);
        }
        if (this.listeners.size === 0) {
            this.controller?.abort();
            this.clientId = undefined;
            return;
        }
        if (this.clientId)
            await this.sync();
    }
    ensureRunning() {
        if (this.running)
            return;
        this.ready = new Promise((resolve) => { this.resolveReady = resolve; });
        this.running = this.run().finally(() => { this.running = undefined; this.ready = undefined; this.resolveReady = undefined; });
    }
    async sync() { if (this.clientId)
        await this.client._request("/api/realtime", { method: "POST", body: JSON.stringify({ clientId: this.clientId, subscriptions: [...this.listeners.keys()] }) }, this.controller?.signal); }
    async run() {
        const controller = new AbortController();
        this.controller = controller;
        while (!controller.signal.aborted && this.listeners.size) {
            try {
                const response = await this.client._eventStream("/api/realtime", this.lastEventId, controller.signal);
                for await (const message of parseEventStream(response.body, controller.signal)) {
                    const data = JSON.parse(message.data);
                    if (message.event === "PB_CONNECT") {
                        this.clientId = String(data.clientId ?? "");
                        if (!this.clientId)
                            throw new PgBaseError(0, "invalid_realtime_connect");
                        await this.sync();
                        this.resolveReady?.();
                        continue;
                    }
                    if (message.id)
                        this.lastEventId = canonicalDeliveryCursor(message.id);
                    const topic = message.event;
                    if (!topic)
                        continue;
                    const event = { topic, action: data.action, record: data.record };
                    if (!event.record || !["create", "update", "delete"].includes(event.action))
                        continue;
                    for (const listener of this.listeners.get(topic) ?? [])
                        listener(event);
                }
            }
            catch (error) {
                if (controller.signal.aborted)
                    return;
                await waitForReconnect(250, controller.signal);
            }
        }
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
    async authenticate(action, email, password, signal) {
        const generation = this.client.auth.generation;
        try {
            const session = await this.client._request(this.path(action), { method: "POST", body: JSON.stringify({ email, password }) }, signal);
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
    register(email, password, signal) { return this.authenticate("register", email, password, signal); }
    login(email, password, signal) { return this.authenticate("login", email, password, signal); }
    /** Returns the sign-in methods enabled for this auth collection. */
    methods(signal) { return this.client._request(this.path("methods"), {}, signal); }
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
        return (await this.client._request(`${this.path("profile")}${parameters.size ? `?${parameters}` : ""}`, {}, requestSignal)).attributes;
    }
    async updateProfile(profile, options, signal) {
        const [query, requestSignal] = recordGetArguments(options, signal);
        const suffix = query?.fields === undefined ? "" : `?fields=${encodeURIComponent(query.fields)}`;
        return (await this.client._request(`${this.path("profile")}${suffix}`, { method: "PATCH", body: JSON.stringify(profile) }, requestSignal)).attributes;
    }
    oauthStart(provider, redirectUri, signal) { return this.client._request(this.path("oauth/start"), { method: "POST", body: JSON.stringify({ provider, redirectUri }) }, signal); }
    /** Starts OAuth and delegates browser/popup/deep-link navigation to the host. */
    async oauthStartAndOpen(provider, redirectUri, options, signal) {
        const started = await this.oauthStart(provider, redirectUri, signal);
        await options.open(started.authorizationUrl);
        return started;
    }
    async oauthWithBrowser(provider, redirectUri, adapter, signal) {
        try {
            const started = await this.oauthStart(provider, redirectUri, signal);
            const expectedState = new URL(started.authorizationUrl).searchParams.get("state");
            if (!expectedState)
                throw new OAuthCallbackError("invalid_start", "The OAuth start URL is missing state.");
            await adapter.open(started.authorizationUrl);
            const callback = await callbackWithAbort(adapter, signal);
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
            return await this.oauthComplete(provider, state, code, signal);
        }
        finally {
            adapter.close?.();
        }
    }
    oauthLink(currentPassword, provider, redirectUri, signal) { return this.client._request("/api/v1/auth/oauth/link", { method: "POST", body: JSON.stringify({ currentPassword, provider, redirectUri }) }, signal); }
    async oauthComplete(provider, state, code, signal) { const generation = this.client.auth.generation; const session = await this.client._request("/api/v1/auth/oauth/complete", { method: "POST", body: JSON.stringify({ provider, state, code }) }, signal); if (session.token && generation === this.client.auth.generation)
        this.client.auth.save(session.token, session.user); return session; }
    oauthCompleteFromURL(provider, callbackURL, signal) {
        const url = typeof callbackURL === "string" ? new URL(callbackURL) : callbackURL;
        const providerError = url.searchParams.get("error");
        if (providerError)
            throw new OAuthCallbackError(providerError, url.searchParams.get("error_description") ?? undefined);
        const state = url.searchParams.get("state");
        const code = url.searchParams.get("code");
        if (!state || !code)
            throw new OAuthCallbackError("invalid_callback", "The OAuth callback is missing state or code.");
        return this.oauthComplete(provider, state, code, signal);
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
        const parameters = new URLSearchParams();
        for (const [key, value] of Object.entries(query)) {
            if (value !== undefined) {
                parameters.set(key, String(value));
            }
        }
        const suffix = parameters.size ? `?${parameters}` : "";
        return this.client._request(`${this.path()}${suffix}`, {}, signal);
    }
    async get(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return (await this.getWithETag(id, options, requestSignal)).record;
    }
    getWithETag(id, optionsOrSignal, signal) {
        const [options, requestSignal] = recordGetArguments(optionsOrSignal, signal);
        return this.recordResponse(this.path(id, options), {}, requestSignal);
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
        }, requestSignal);
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
        }, requestSignal);
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
    async recordResponse(path, init, signal) {
        const response = await this.client._response(path, init, signal);
        return { record: response.body, etag: response.etag };
    }
}
function recordGetArguments(optionsOrSignal, signal) {
    if (optionsOrSignal && "aborted" in optionsOrSignal)
        return [undefined, optionsOrSignal];
    return [optionsOrSignal, signal];
}
function canonicalDeliveryCursor(value) {
    if (typeof value === "number" && !Number.isSafeInteger(value))
        throw new PgBaseError(0, "invalid_cursor");
    const text = String(value);
    if (!/^\d+$/.test(text))
        throw new PgBaseError(0, "invalid_cursor");
    const cursor = BigInt(text);
    if (cursor > 9223372036854775807n)
        throw new PgBaseError(0, "invalid_cursor");
    return cursor.toString();
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
async function* parseEventStream(body, signal) {
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
    }
    finally {
        reader.releaseLock();
    }
}
function parseSSEMessage(raw) {
    const message = { data: "" };
    for (const line of raw.split(/\r?\n/)) {
        if (line.startsWith("event:")) {
            message.event = line.slice(6).trim();
        }
        else if (line.startsWith("id:")) {
            message.id = line.slice(3).trim();
        }
        else if (line.startsWith("data:")) {
            message.data += `${message.data ? "\n" : ""}${line.slice(5).trimStart()}`;
        }
    }
    return message.data ? message : undefined;
}
function deliveryCursor(message) {
    if (!message.id || !/^\d+$/.test(message.id)) {
        return undefined;
    }
    try {
        const cursor = BigInt(message.id);
        return cursor <= 9223372036854775807n ? cursor.toString() : undefined;
    }
    catch {
        return undefined;
    }
}
function recordEvent(message, deliveryId) {
    if (message.event !== "record") {
        return undefined;
    }
    try {
        const value = JSON.parse(message.data);
        if (!value.eventId || typeof value.eventId !== "string") {
            return undefined;
        }
        return { ...value, deliveryId };
    }
    catch {
        return undefined;
    }
}
function waitForReconnect(delay, signal) {
    return new Promise((resolve) => {
        const timer = setTimeout(resolve, delay);
        signal?.addEventListener("abort", () => {
            clearTimeout(timer);
            resolve();
        }, { once: true });
    });
}
