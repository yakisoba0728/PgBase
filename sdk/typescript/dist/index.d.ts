export * from "./admin.js";
export type User = {
    id: string;
    email: string;
    createdAt: string;
    verified?: boolean;
    authCollectionId?: string;
    authCollection?: string;
};
export type UserSession = {
    token?: string;
    user: User;
    expiresAt: string;
    impersonated?: boolean;
    impersonatingAdministratorId?: string;
};
export type GeoPoint = {
    lon: number;
    lat: number;
};
export type RecordData = Record<string, unknown>;
export type RecordPage<T extends RecordData> = {
    items: T[];
    nextCursor?: string;
};
export type RecordResult<T extends RecordData> = {
    record: T;
    etag?: string;
};
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
    /** Comma-separated record fields requested for realtime payloads. */
    fields?: string;
    lastEventId?: string | number;
    signal?: AbortSignal;
    maxReconnects?: number;
    reconnectDelayMs?: number;
};
export type RealtimeEvent<T extends RecordData = RecordData> = {
    action: "create" | "update" | "delete";
    record: T;
    topic: string;
};
export type RequestOptions = {
    signal?: AbortSignal;
    requestKey?: string;
};
/** Opens the provider URL using a browser popup, redirect, or mobile launcher. */
export type OAuthLaunchOptions = {
    open(url: string): void | Promise<void>;
};
export type OAuthBrowserAdapter = OAuthLaunchOptions & {
    waitForCallback(signal?: AbortSignal): Promise<string | URL>;
    close?(): void;
};
export type BatchOperation<T extends RecordData = RecordData> = {
    operation: "create";
    collection: string;
    input: Omit<T, "id">;
    idempotencyKey?: string;
} | {
    operation: "update";
    collection: string;
    id: string;
    input: Partial<T>;
    ifMatch?: string;
} | BatchUpsertOperation<T> | {
    operation: "delete";
    collection: string;
    id: string;
    ifMatch?: string;
};
/** An atomic PUT-equivalent batch operation: creates the ID when absent, otherwise updates it. */
export type BatchUpsertOperation<T extends RecordData = RecordData> = {
    operation: "upsert";
    collection: string;
    id: string;
    input: Partial<T>;
    ifMatch?: string;
};
export type BatchResult<T extends RecordData = RecordData> = {
    eventId: string;
    record?: T;
    etag?: string;
    version?: number;
};
export type BatchHTTPRequest<T extends RecordData = RecordData> = {
    method: "POST" | "PATCH" | "PUT" | "DELETE";
    url: string;
    body?: Partial<T>;
    headers?: Record<string, string>;
};
export type BatchHTTPResult<T extends RecordData = RecordData> = {
    status: number;
    headers?: Record<string, string>;
    body?: T;
    eventId: string;
    etag?: string;
    version?: number;
};
export type BatchFilePart = {
    blob: Blob;
    filename: string;
};
export type FileInfo = {
    id: string;
    field: string;
    filename: string;
    contentType: string;
    size: number;
    thumbnail: boolean;
};
export type MFAChallenge = {
    challengeToken: string;
    expiresAt: string;
};
/** Enabled sign-in methods for one explicit public auth collection. */
export type AuthMethods = {
    passwordEnabled: boolean;
    otpEnabled: boolean;
    mfaEnabled: boolean;
    oauthProviders: string[];
};
export declare class PgBaseError extends Error {
    readonly status: number;
    readonly code: string;
    readonly details: Record<string, string[]>;
    readonly etag?: string | undefined;
    constructor(status: number, code: string, details?: Record<string, string[]>, etag?: string | undefined);
}
export declare class MFAChallengeError extends PgBaseError {
    readonly challenge: MFAChallenge;
    constructor(status: number, code: string, details: Record<string, string[]>, challenge: MFAChallenge);
}
export interface TokenStorage {
    get(): string | null;
    set(token: string): void;
    clear(): void;
}
/** A serialized PocketBase-compatible auth state for SSR hand-off. */
export type AuthState = {
    token: string | null;
    record: RecordData | null;
};
export declare class AuthStore {
    #private;
    private readonly storage?;
    constructor(storage?: TokenStorage | undefined);
    get token(): string | null;
    get record(): RecordData | null;
    get isValid(): boolean;
    get generation(): number;
    save(token: string, record?: RecordData | null): void;
    clear(): void;
    /** Encodes token and cached auth record for a server-rendered response cookie. */
    exportToCookie(options?: {
        key?: string;
        httpOnly?: boolean;
        secure?: boolean;
        sameSite?: "Lax" | "Strict" | "None";
        path?: string;
    }): string;
    /** Loads a state exported by [exportToCookie]; malformed cookies clear state. */
    loadFromCookie(cookie: string, key?: string): void;
    _onChange(listener: (generation: number) => void): () => void;
    private notify;
}
/**
 * Wrap browser storage only when the caller deliberately supplies it. This
 * keeps token persistence opt-in and makes the storage lifecycle visible at
 * client construction.
 */
export declare function browserTokenStorage(storage: Storage, key?: string): TokenStorage;
export declare class PgBaseClient {
    readonly baseURL: string;
    readonly auth: AuthStore;
    readonly realtime: RealtimeService;
    private readonly fetcher;
    private readonly requestControllers;
    /** Matches PocketBase's opt-out switch for duplicate request cancellation. */
    autoCancellation: boolean;
    constructor(baseURL: string, options?: {
        fetch?: typeof fetch;
        auth?: AuthStore;
    });
    register(email: string, password: string, signal?: AbortSignal): Promise<UserSession>;
    /** Binds authentication calls to an explicit auth realm without changing record APIs. */
    realm(name: string): AuthRealm;
    login(email: string, password: string, signal?: AbortSignal): Promise<UserSession>;
    session(signal?: AbortSignal): Promise<UserSession>;
    logout(signal?: AbortSignal): Promise<void>;
    changePassword(currentPassword: string, newPassword: string, signal?: AbortSignal): Promise<void>;
    requestPasswordReset(email: string, signal?: AbortSignal): Promise<void>;
    requestOTP(email: string, signal?: AbortSignal): Promise<{
        otpId: string;
        expiresAt: string;
    }>;
    completeOTP(otpId: string, code: string, signal?: AbortSignal): Promise<UserSession>;
    confirmPasswordReset(token: string, newPassword: string, signal?: AbortSignal): Promise<void>;
    requestVerification(signal?: AbortSignal): Promise<void>;
    confirmVerification(token: string, signal?: AbortSignal): Promise<void>;
    requestEmailChange(currentPassword: string, newEmail: string, signal?: AbortSignal): Promise<void>;
    confirmEmailChange(token: string, signal?: AbortSignal): Promise<void>;
    records<T extends RecordData = RecordData>(collection: string): Records<T>;
    batch<T extends RecordData = RecordData>(operations: BatchOperation<T>[], options?: BatchOptions | AbortSignal, signal?: AbortSignal): Promise<{
        results: BatchResult<T>[];
    }>;
    batchRequests<T extends RecordData = RecordData>(requests: BatchHTTPRequest<T>[], options?: BatchOptions | AbortSignal, signal?: AbortSignal): Promise<{
        results: BatchHTTPResult<T>[];
    }>;
    batchMultipart<T extends RecordData = RecordData>(requests: BatchHTTPRequest<T>[], files: Record<string, BatchFilePart>, signal?: AbortSignal): Promise<{
        results: BatchHTTPResult<T>[];
    }>;
    files(collection: string, recordID: string): Files;
    completeMFA(challengeToken: string, code: string, signal?: AbortSignal): Promise<UserSession>;
    beginTOTPEnrollment(currentPassword: string, signal?: AbortSignal): Promise<{
        secret: string;
        otpAuthUrl: string;
    }>;
    confirmTOTPEnrollment(code: string, signal?: AbortSignal): Promise<{
        codes: string[];
    }>;
    iterate<T extends RecordData>(collection: string, query?: Query, signal?: AbortSignal): AsyncGenerator<T>;
    _request<T>(path: string, init?: RequestInit, signal?: AbortSignal): Promise<T>;
    /** Cancels an in-flight request started with this key. */
    cancelRequest(requestKey: string): void;
    /** Cancels every keyed in-flight request. */
    cancelAllRequests(): void;
    /** Low-level request with PocketBase-style request-key auto-cancellation. */
    send<T>(path: string, init?: RequestInit, options?: RequestOptions): Promise<T>;
    _response<T>(path: string, init?: RequestInit, signal?: AbortSignal): Promise<{
        body: T;
        etag?: string;
    }>;
    _eventStream(path: string, lastEventId: string | undefined, signal?: AbortSignal): Promise<Response>;
    private authenticate;
    private request;
    private fetchResponse;
    private keyedSignal;
}
/** Multiplexes PocketBase-compatible realtime topics over one durable SSE connection. */
export declare class RealtimeService {
    private readonly client;
    private readonly listeners;
    private controller?;
    private clientId?;
    private lastEventId?;
    private running?;
    private ready?;
    private resolveReady?;
    constructor(client: PgBaseClient);
    subscribe(topic: string, listener: (event: RealtimeEvent) => void): Promise<() => Promise<void>>;
    unsubscribe(topic?: string, listener?: (event: RealtimeEvent) => void): Promise<void>;
    private ensureRunning;
    private sync;
    private run;
}
export declare class AuthRealm {
    private readonly client;
    readonly name: string;
    constructor(client: PgBaseClient, name: string);
    private path;
    private authenticate;
    register(email: string, password: string, signal?: AbortSignal): Promise<UserSession>;
    login(email: string, password: string, signal?: AbortSignal): Promise<UserSession>;
    /** Returns the sign-in methods enabled for this auth collection. */
    methods(signal?: AbortSignal): Promise<AuthMethods>;
    /** Rotates the current bearer token after validating its replacement session. */
    refresh(signal?: AbortSignal): Promise<UserSession>;
    session(signal?: AbortSignal): Promise<UserSession>;
    logout(signal?: AbortSignal): Promise<void>;
    requestPasswordReset(email: string, signal?: AbortSignal): Promise<void>;
    requestOTP(email: string, signal?: AbortSignal): Promise<{
        otpId: string;
        expiresAt: string;
    }>;
    completeOTP(otpId: string, code: string, signal?: AbortSignal): Promise<UserSession>;
    profile<T extends RecordData = RecordData>(options?: RecordGetOptions | AbortSignal, signal?: AbortSignal): Promise<T>;
    updateProfile<T extends RecordData = RecordData>(profile: T, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T>;
    oauthStart(provider: string, redirectUri: string, signal?: AbortSignal): Promise<{
        authorizationUrl: string;
        expiresAt: string;
    }>;
    /** Starts OAuth and delegates browser/popup/deep-link navigation to the host. */
    oauthStartAndOpen(provider: string, redirectUri: string, options: OAuthLaunchOptions, signal?: AbortSignal): Promise<{
        authorizationUrl: string;
        expiresAt: string;
    }>;
    oauthWithBrowser(provider: string, redirectUri: string, adapter: OAuthBrowserAdapter, signal?: AbortSignal): Promise<UserSession>;
    oauthLink(currentPassword: string, provider: string, redirectUri: string, signal?: AbortSignal): Promise<{
        authorizationUrl: string;
        expiresAt: string;
    }>;
    oauthComplete(provider: string, state: string, code: string, signal?: AbortSignal): Promise<UserSession>;
    oauthCompleteFromURL(provider: string, callbackURL: string | URL, signal?: AbortSignal): Promise<UserSession>;
}
export declare class OAuthCallbackError extends Error {
    readonly code: string;
    constructor(code: string, message?: string);
}
export declare class Records<T extends RecordData> {
    private readonly client;
    private readonly collection;
    constructor(client: PgBaseClient, collection: string);
    list(query?: Query, signal?: AbortSignal): Promise<RecordPage<T>>;
    get(id: string, signal?: AbortSignal): Promise<T>;
    get(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<T>;
    getWithETag(id: string, signal?: AbortSignal): Promise<RecordResult<T>>;
    getWithETag(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<RecordResult<T>>;
    create(data: Omit<T, "id">, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T>;
    createWithETag(data: Omit<T, "id">, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>>;
    update(id: string, data: Partial<T>, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T>;
    updateWithETag(id: string, data: Partial<T>, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>>;
    remove(id: string, etag: string, signal?: AbortSignal): Promise<void>;
    events(options?: EventOptions): AsyncGenerator<RecordEvent>;
    private path;
    private eventsPath;
    private recordResponse;
}
export declare class Files {
    private readonly client;
    private readonly collection;
    private readonly recordID;
    constructor(client: PgBaseClient, collection: string, recordID: string);
    list(field: string, signal?: AbortSignal): Promise<{
        items: FileInfo[];
    }>;
    upload(field: string, file: Blob, filename: string, signal?: AbortSignal): Promise<FileInfo>;
    delete(fileID: string, signal?: AbortSignal): Promise<void>;
    mintDownloadToken(fileID: string, variant?: string, signal?: AbortSignal): Promise<{
        token: string;
    }>;
    downloadURL(fileID: string, token: string, variant?: string): string;
    private fieldPath;
    private filePath;
}
