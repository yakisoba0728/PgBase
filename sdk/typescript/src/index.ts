export type User = { id: string; email: string; createdAt: string; verified?: boolean; authCollectionId?: string; authCollection?: string };
export type UserSession = { token?: string; user: User; expiresAt: string };
export type RecordData = Record<string, unknown>;
export type RecordPage<T extends RecordData> = { items: T[]; nextCursor?: string };
export type RecordResult<T extends RecordData> = { record: T; etag?: string };
export type Query = {
  filter?: string;
  sort?: string;
  expand?: string;
  cursor?: string;
  limit?: number;
};
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
export type BatchOperation<T extends RecordData = RecordData> =
  | { operation: "create"; collection: string; input: Omit<T, "id">; idempotencyKey?: string }
  | { operation: "update"; collection: string; id: string; input: Partial<T>; ifMatch?: string }
  | { operation: "delete"; collection: string; id: string; ifMatch?: string };
export type BatchResult<T extends RecordData = RecordData> = { eventId: string; record?: T; etag?: string; version?: number };
export type FileInfo = { id: string; field: string; filename: string; contentType: string; size: number; thumbnail: boolean };
export type MFAChallenge = { challengeToken: string; expiresAt: string };

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

export class AuthStore {
  #token: string | null;
  #generation = 0;
  #listeners = new Set<(generation: number) => void>();

  constructor(private readonly storage?: TokenStorage) {
    this.#token = storage?.get() ?? null;
  }

  get token(): string | null {
    return this.#token;
  }

  get generation(): number {
    return this.#generation;
  }

  save(token: string): void {
    this.#token = token;
    this.#generation += 1;
    this.storage?.set(token);
    this.notify();
  }

  clear(): void {
    this.#token = null;
    this.#generation += 1;
    this.storage?.clear();
    this.notify();
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

  batch<T extends RecordData = RecordData>(operations: BatchOperation<T>[], signal?: AbortSignal): Promise<{ results: BatchResult<T>[] }> {
    return this.request("/api/v1/user/collections/batch", { method: "POST", body: JSON.stringify({ operations }) }, signal);
  }

  files(collection: string, recordID: string): Files {
    return new Files(this, collection, recordID);
  }

  async completeMFA(challengeToken: string, code: string, signal?: AbortSignal): Promise<UserSession> {
    const generation = this.auth.generation;
    const session = await this.request<UserSession>("/api/v1/auth/mfa/complete", { method: "POST", body: JSON.stringify({ challengeToken, code }) }, signal);
    if (session.token && generation === this.auth.generation) this.auth.save(session.token);
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
      this.auth.save(session.token);
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
}

export class AuthRealm {
  constructor(private readonly client: PgBaseClient, readonly name: string) {}
  private path(action: string): string { return `/api/v1/auth/${encodeURIComponent(this.name)}/${action}`; }
  private async authenticate(action: "register" | "login", email: string, password: string, signal?: AbortSignal): Promise<UserSession> {
    const generation = this.client.auth.generation;
    try {
      const session = await this.client._request<UserSession>(this.path(action), { method: "POST", body: JSON.stringify({ email, password }) }, signal);
      if (session.token && generation === this.client.auth.generation) this.client.auth.save(session.token);
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
  session(signal?: AbortSignal): Promise<UserSession> { return this.client._request(this.path("session"), {}, signal); }
  async logout(signal?: AbortSignal): Promise<void> { try { await this.client._request<void>(this.path("logout"), { method: "POST" }, signal); } finally { this.client.auth.clear(); } }
  requestPasswordReset(email: string, signal?: AbortSignal): Promise<void> { return this.client._request(this.path("request-password-reset"), { method: "POST", body: JSON.stringify({ email }) }, signal); }
  async profile<T extends RecordData = RecordData>(signal?: AbortSignal): Promise<T> { return (await this.client._request<{ attributes: T }>(this.path("profile"), {}, signal)).attributes; }
  async updateProfile<T extends RecordData = RecordData>(profile: T, signal?: AbortSignal): Promise<T> { return (await this.client._request<{ attributes: T }>(this.path("profile"), { method: "PATCH", body: JSON.stringify(profile) }, signal)).attributes; }
  oauthStart(provider: string, redirectUri: string, signal?: AbortSignal): Promise<{ authorizationUrl: string; expiresAt: string }> { return this.client._request(this.path("oauth/start"), { method: "POST", body: JSON.stringify({ provider, redirectUri }) }, signal); }
  oauthLink(currentPassword: string, provider: string, redirectUri: string, signal?: AbortSignal): Promise<{ authorizationUrl: string; expiresAt: string }> { return this.client._request("/api/v1/auth/oauth/link", { method: "POST", body: JSON.stringify({ currentPassword, provider, redirectUri }) }, signal); }
  async oauthComplete(provider: string, state: string, code: string, signal?: AbortSignal): Promise<UserSession> { const generation = this.client.auth.generation; const session = await this.client._request<UserSession>("/api/v1/auth/oauth/complete", { method: "POST", body: JSON.stringify({ provider, state, code }) }, signal); if (session.token && generation === this.client.auth.generation) this.client.auth.save(session.token); return session; }
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

  async get(id: string, signal?: AbortSignal): Promise<T> {
    return (await this.getWithETag(id, signal)).record;
  }

  getWithETag(id: string, signal?: AbortSignal): Promise<RecordResult<T>> { return this.recordResponse(this.path(id), {}, signal); }

  async create(data: Omit<T, "id">, idempotencyKey: string, signal?: AbortSignal): Promise<T> {
    return (await this.createWithETag(data, idempotencyKey, signal)).record;
  }

  createWithETag(data: Omit<T, "id">, idempotencyKey: string, signal?: AbortSignal): Promise<RecordResult<T>> {
    return this.recordResponse(this.path(), {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: JSON.stringify(data),
    }, signal);
  }

  async update(id: string, data: Partial<T>, etag: string, signal?: AbortSignal): Promise<T> {
    return (await this.updateWithETag(id, data, etag, signal)).record;
  }

  updateWithETag(id: string, data: Partial<T>, etag: string, signal?: AbortSignal): Promise<RecordResult<T>> {
    return this.recordResponse(this.path(id), {
      method: "PATCH",
      headers: { "If-Match": etag },
      body: JSON.stringify(data),
    }, signal);
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

  private path(id?: string): string {
    const base = `/api/v1/user/collections/${encodeURIComponent(this.collection)}/records`;
    return id ? `${base}/${encodeURIComponent(id)}` : base;
  }

  private eventsPath(): string {
    return `/api/v1/user/collections/${encodeURIComponent(this.collection)}/events`;
  }

  private async recordResponse(path: string, init: RequestInit, signal?: AbortSignal): Promise<RecordResult<T>> {
    const response = await this.client._response<T>(path, init, signal);
    return { record: response.body, etag: response.etag };
  }
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
