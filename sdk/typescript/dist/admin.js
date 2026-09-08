export class AdminAPIError extends Error {
    status;
    code;
    details;
    constructor(status, code, details = {}) {
        super(code);
        this.status = status;
        this.code = code;
        this.details = details;
        this.name = "AdminAPIError";
    }
}
/** The preview binds the exact request body and replay identity. Inspect the
 * copy returned by preview; editing it cannot change what apply submits. */
export class ApprovedSchemaPlan {
    #body;
    #preview;
    #operationID = crypto.randomUUID();
    #idempotencyKey = crypto.randomUUID();
    constructor(change, plan) {
        this.#body = JSON.stringify({ ...change, checksum: plan.checksum, baseVersion: plan.baseVersion });
        this.#preview = JSON.stringify(plan);
    }
    get preview() { return JSON.parse(this.#preview); }
    get operationID() { return this.#operationID; }
    request() {
        return { body: this.#body, headers: { "PgBase-Operation-ID": this.#operationID, "Idempotency-Key": this.#idempotencyKey } };
    }
}
/** Browser administrator client. Cookies are managed by the browser and CSRF
 * tokens stay in memory. Use a same-origin URL; a public user token is a
 * separate identity and is never attached to administrator requests. */
export class PgBaseAdminClient {
    baseURL;
    #csrf = "";
    #generation = 0;
    #fetch;
    constructor(baseURL, options = {}) {
        this.baseURL = baseURL;
        this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
    }
    async login(email, password, signal) {
        return this.adopt("/api/admin/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }, signal);
    }
    resume(signal) { return this.adopt("/api/admin/session", {}, signal); }
    async logout(signal) {
        const generation = this.#generation;
        await this.request("/api/admin/auth/logout", { method: "POST" }, signal);
        if (generation === this.#generation) {
            this.#csrf = "";
            this.#generation++;
        }
    }
    catalog(signal) { return this.request("/api/admin/collections", {}, signal); }
    async plan(change, signal) {
        // Snapshot before awaiting: caller edits while the preview runs must not
        // bind a different change to the server's returned checksum.
        const body = JSON.stringify(change);
        const plan = await this.request("/api/admin/collections/plan", { method: "POST", body }, signal);
        return new ApprovedSchemaPlan(JSON.parse(body), plan);
    }
    apply(plan, signal) {
        return this.request("/api/admin/collections/apply", { method: "POST", ...plan.request() }, signal);
    }
    operation(id, signal) { return this.request(`/api/admin/schema-operations/${encodeURIComponent(id)}`, {}, signal); }
    logs(options = {}, signal) {
        const query = new URLSearchParams();
        if (options.before !== undefined)
            query.set("before", String(options.before));
        if (options.statusClass !== undefined)
            query.set("statusClass", String(options.statusClass));
        return this.request(`/api/admin/logs?${query}`, {}, signal);
    }
    jobs(signal) { return this.request("/api/admin/jobs", {}, signal); }
    configureJob(id, schedule, enabled, signal) { return this.request(`/api/admin/jobs/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify({ schedule, enabled }) }, signal); }
    queueJob(id, signal) { return this.request(`/api/admin/jobs/${encodeURIComponent(id)}/run`, { method: "POST" }, signal); }
    async adopt(path, init, signal) {
        const generation = ++this.#generation;
        const session = await this.request(path, init, signal, true);
        if (!session.csrfToken)
            throw new AdminAPIError(0, "invalid_session_response");
        if (generation === this.#generation)
            this.#csrf = session.csrfToken;
        return { administrator: session.administrator, expiresAt: session.expiresAt };
    }
    async request(path, init, signal, anonymous = false) {
        const generation = this.#generation;
        const headers = new Headers(init.headers);
        headers.set("Accept", "application/json");
        if (init.body !== undefined)
            headers.set("Content-Type", "application/json");
        if (!anonymous && init.method && init.method !== "GET")
            headers.set("X-PgBase-CSRF", this.#csrf);
        const response = await this.#fetch(new URL(path, this.baseURL), { ...init, headers, credentials: "include", signal });
        if (!response.ok) {
            if (response.status === 401 && generation === this.#generation) {
                this.#csrf = "";
                this.#generation++;
            }
            const data = await response.json().catch(() => undefined);
            throw new AdminAPIError(response.status, data?.error?.code ?? "request_failed", data?.error?.details);
        }
        if (response.status === 204)
            return undefined;
        try {
            return await response.json();
        }
        catch {
            throw new AdminAPIError(response.status, "invalid_response_body");
        }
    }
}
