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
    #generation = 0;
    #listeners = new Set();
    constructor(storage) {
        this.storage = storage;
        this.#token = storage?.get() ?? null;
    }
    get token() {
        return this.#token;
    }
    get generation() {
        return this.#generation;
    }
    save(token) {
        this.#token = token;
        this.#generation += 1;
        this.storage?.set(token);
        this.notify();
    }
    clear() {
        this.#token = null;
        this.#generation += 1;
        this.storage?.clear();
        this.notify();
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
    fetcher;
    constructor(baseURL, options = {}) {
        this.baseURL = baseURL;
        this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
        this.auth = options.auth ?? new AuthStore();
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
                this.auth.save(session.token);
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
    batch(operations, signal) {
        return this.request("/api/v1/user/collections/batch", { method: "POST", body: JSON.stringify({ operations }) }, signal);
    }
    batchRequests(requests, signal) {
        return this.request("/api/v1/user/collections/batch", { method: "POST", body: JSON.stringify({ requests }) }, signal);
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
            this.auth.save(session.token);
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
            this.auth.save(session.token);
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
                this.client.auth.save(session.token);
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
            this.client.auth.save(session.token);
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
                this.client.auth.save(session.token);
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
    async profile(signal) { return (await this.client._request(this.path("profile"), {}, signal)).attributes; }
    async updateProfile(profile, signal) { return (await this.client._request(this.path("profile"), { method: "PATCH", body: JSON.stringify(profile) }, signal)).attributes; }
    oauthStart(provider, redirectUri, signal) { return this.client._request(this.path("oauth/start"), { method: "POST", body: JSON.stringify({ provider, redirectUri }) }, signal); }
    oauthLink(currentPassword, provider, redirectUri, signal) { return this.client._request("/api/v1/auth/oauth/link", { method: "POST", body: JSON.stringify({ currentPassword, provider, redirectUri }) }, signal); }
    async oauthComplete(provider, state, code, signal) { const generation = this.client.auth.generation; const session = await this.client._request("/api/v1/auth/oauth/complete", { method: "POST", body: JSON.stringify({ provider, state, code }) }, signal); if (session.token && generation === this.client.auth.generation)
        this.client.auth.save(session.token); return session; }
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
    async create(data, idempotencyKey, signal) {
        return (await this.createWithETag(data, idempotencyKey, signal)).record;
    }
    createWithETag(data, idempotencyKey, signal) {
        return this.recordResponse(this.path(), {
            method: "POST",
            headers: { "Idempotency-Key": idempotencyKey },
            body: JSON.stringify(data),
        }, signal);
    }
    async update(id, data, etag, signal) {
        return (await this.updateWithETag(id, data, etag, signal)).record;
    }
    updateWithETag(id, data, etag, signal) {
        return this.recordResponse(this.path(id), {
            method: "PATCH",
            headers: { "If-Match": etag },
            body: JSON.stringify(data),
        }, signal);
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
        if (!id)
            return base;
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
