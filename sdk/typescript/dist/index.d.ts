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
} | {
    operation: "delete";
    collection: string;
    id: string;
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
export declare class AuthStore {
    #private;
    private readonly storage?;
    constructor(storage?: TokenStorage | undefined);
    get token(): string | null;
    get generation(): number;
    save(token: string): void;
    clear(): void;
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
    private readonly fetcher;
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
    confirmPasswordReset(token: string, newPassword: string, signal?: AbortSignal): Promise<void>;
    requestVerification(signal?: AbortSignal): Promise<void>;
    confirmVerification(token: string, signal?: AbortSignal): Promise<void>;
    requestEmailChange(currentPassword: string, newEmail: string, signal?: AbortSignal): Promise<void>;
    confirmEmailChange(token: string, signal?: AbortSignal): Promise<void>;
    records<T extends RecordData = RecordData>(collection: string): Records<T>;
    batch<T extends RecordData = RecordData>(operations: BatchOperation<T>[], signal?: AbortSignal): Promise<{
        results: BatchResult<T>[];
    }>;
    batchRequests<T extends RecordData = RecordData>(requests: BatchHTTPRequest<T>[], signal?: AbortSignal): Promise<{
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
    _response<T>(path: string, init?: RequestInit, signal?: AbortSignal): Promise<{
        body: T;
        etag?: string;
    }>;
    _eventStream(path: string, lastEventId: string | undefined, signal?: AbortSignal): Promise<Response>;
    private authenticate;
    private request;
    private fetchResponse;
}
export declare class AuthRealm {
    private readonly client;
    readonly name: string;
    constructor(client: PgBaseClient, name: string);
    private path;
    private authenticate;
    register(email: string, password: string, signal?: AbortSignal): Promise<UserSession>;
    login(email: string, password: string, signal?: AbortSignal): Promise<UserSession>;
    session(signal?: AbortSignal): Promise<UserSession>;
    logout(signal?: AbortSignal): Promise<void>;
    requestPasswordReset(email: string, signal?: AbortSignal): Promise<void>;
    profile<T extends RecordData = RecordData>(signal?: AbortSignal): Promise<T>;
    updateProfile<T extends RecordData = RecordData>(profile: T, signal?: AbortSignal): Promise<T>;
    oauthStart(provider: string, redirectUri: string, signal?: AbortSignal): Promise<{
        authorizationUrl: string;
        expiresAt: string;
    }>;
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
    getWithETag(id: string, signal?: AbortSignal): Promise<RecordResult<T>>;
    create(data: Omit<T, "id">, idempotencyKey: string, signal?: AbortSignal): Promise<T>;
    createWithETag(data: Omit<T, "id">, idempotencyKey: string, signal?: AbortSignal): Promise<RecordResult<T>>;
    update(id: string, data: Partial<T>, etag: string, signal?: AbortSignal): Promise<T>;
    updateWithETag(id: string, data: Partial<T>, etag: string, signal?: AbortSignal): Promise<RecordResult<T>>;
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
