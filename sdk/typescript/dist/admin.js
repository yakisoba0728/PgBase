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
/** A preview plus the exact JSON document that produced it. The apply body is
 * built from this private snapshot so large JSON integers survive preview. */
export class ApprovedSchemaImportPlan {
    baseVersion;
    checksum;
    policyChecksum;
    changes;
    document;
    operationId = crypto.randomUUID();
    idempotencyKey = crypto.randomUUID();
    #applyBody;
    constructor(preview, documentJSON) {
        this.baseVersion = preview.baseVersion;
        this.checksum = preview.checksum;
        this.policyChecksum = preview.policyChecksum;
        this.changes = preview.changes;
        this.document = preview.document;
        this.#applyBody = `{"document":${documentJSON},"baseVersion":${JSON.stringify(preview.baseVersion)},"checksum":${JSON.stringify(preview.checksum)}}`;
    }
    request() {
        return { body: this.#applyBody, headers: { "PgBase-Operation-ID": this.operationId, "Idempotency-Key": this.idempotencyKey } };
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
    generateAppleClientSecret(input, signal) {
        return this.request("/api/admin/settings/apple/generate-client-secret", { method: "POST", body: JSON.stringify(input) }, signal);
    }
    scaffolds(signal) { return this.request("/api/admin/collections/scaffolds", {}, signal); }
    truncateCollection(name, signal) {
        return this.request(`/api/admin/collections/${encodeURIComponent(name)}/truncate`, { method: "POST", body: JSON.stringify({ name }) }, signal);
    }
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
    exportSchema(signal) { return this.request("/api/admin/schema/export", {}, signal); }
    exportSchemaJSON(signal) { return this.requestText("/api/admin/schema/export", {}, signal); }
    async previewSchemaImport(document, signal) {
        const raw = JSON.stringify(document);
        return new ApprovedSchemaImportPlan(await this.request("/api/admin/schema/import/preview", { method: "POST", body: raw }, signal), raw);
    }
    async previewSchemaImportJSON(documentJSON, signal) {
        return new ApprovedSchemaImportPlan(await this.request("/api/admin/schema/import/preview", { method: "POST", body: documentJSON }, signal), documentJSON);
    }
    applySchemaImport(plan, signal) {
        const request = plan instanceof ApprovedSchemaImportPlan ? plan.request() : { headers: { "PgBase-Operation-ID": plan.operationId ?? "", "Idempotency-Key": plan.idempotencyKey ?? "" }, body: JSON.stringify({ document: plan.document, baseVersion: plan.baseVersion, checksum: plan.checksum }) };
        return this.request("/api/admin/schema/import/apply", { method: "POST", ...request }, signal);
    }
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
    fileStorageSettings(signal) { return this.request("/api/admin/storage/files", {}, signal); }
    updateFileStorage(input, signal) { return this.request("/api/admin/storage/files", { method: "PUT", body: JSON.stringify(input) }, signal); }
    async testFileStorage(signal) { await this.request("/api/admin/storage/files/test", { method: "POST" }, signal); }
    backupStorageSettings(signal) { return this.request("/api/admin/storage/backups", {}, signal); }
    updateBackupStorage(input, signal) { return this.request("/api/admin/storage/backups", { method: "PUT", body: JSON.stringify(input) }, signal); }
    async testBackupStorage(signal) { await this.request("/api/admin/storage/backups/test", { method: "POST" }, signal); }
    previewFileStorageMigration(input, signal) { return this.request("/api/admin/storage/files/migrations/preview", { method: "POST", body: JSON.stringify(input) }, signal); }
    startFileStorageMigration(input, operationID, signal) { return this.request("/api/admin/storage/files/migrations", { method: "POST", body: JSON.stringify(input), headers: { "PgBase-Operation-ID": operationID } }, signal); }
    fileStorageMigration(id, signal) { return this.request(`/api/admin/storage/files/migrations/${encodeURIComponent(id)}`, {}, signal); }
    latestFileStorageMigration(signal) { return this.request("/api/admin/storage/files/migrations/latest", {}, signal); }
    async resumeFileStorageMigration(id, signal) { await this.request(`/api/admin/storage/files/migrations/${encodeURIComponent(id)}/resume`, { method: "POST" }, signal); }
    async cancelFileStorageMigration(id, signal) { await this.request(`/api/admin/storage/files/migrations/${encodeURIComponent(id)}/cancel`, { method: "POST" }, signal); }
    impersonateUser(id, durationSeconds = 300, signal) {
        return this.request(`/api/admin/users/${encodeURIComponent(id)}/impersonate`, { method: "POST", body: JSON.stringify({ durationSeconds }) }, signal);
    }
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
    async requestText(path, init, signal) {
        const headers = new Headers(init.headers);
        headers.set("Accept", "application/json");
        if (init.body !== undefined)
            headers.set("Content-Type", "application/json");
        if (init.method && init.method !== "GET")
            headers.set("X-PgBase-CSRF", this.#csrf);
        const response = await this.#fetch(new URL(path, this.baseURL), { ...init, headers, credentials: "include", signal });
        const text = await response.text();
        if (!response.ok)
            throw new AdminAPIError(response.status, "request_failed");
        return text;
    }
}
