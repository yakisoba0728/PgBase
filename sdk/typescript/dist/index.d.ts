export * from "./types.js";
export * from "./auth-store.js";
export { RealtimeService } from "./realtime.js";
import { AuthStore } from "./auth-store.js";
import { RealtimeService } from "./realtime.js";
import type { AuthCreationOptions } from "./types.js";
import type { UserSession, RecordData, FilterParams, RecordPage, PocketBaseListOptions, PocketBasePage, RecordModifiers, RecordMutation, RecordResult, RecordJSONResult, Query, RecordGetOptions, RecordWriteOptions, BatchOptions, RecordEvent, EventOptions, RequestOptions, OAuthLaunchOptions, OAuthBrowserAdapter, BatchOperation, BatchResult, BatchHTTPRequest, BatchHTTPResult, BatchFilePart, FileInfo, AuthMethods, OAuthStart } from "./types.js";
export * from "./admin.js";
export { browserAuthStorage } from "./state-storage.js";
export type { AuthState, AuthStateStorage } from "./state-storage.js";
export declare class PgBaseClient {
    readonly baseURL: string;
    readonly auth: AuthStore;
    readonly realtime: RealtimeService;
    private readonly fetcher;
    private readonly requestControllers;
    /** Cancel older reads with the same request URL, or requests with an explicit key. */
    autoCancellation: boolean;
    constructor(baseURL: string, options?: {
        fetch?: typeof fetch;
        auth?: AuthStore;
    });
    register<T extends RecordData = RecordData>(email: string, password: string, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<UserSession>;
    /** Binds authentication calls to an explicit auth realm without changing record APIs. */
    realm(name: string): AuthRealm;
    login(email: string, password: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<UserSession>;
    session(options?: RecordGetOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>>;
    logout(signal?: AbortSignal): Promise<void>;
    changePassword(currentPassword: string, newPassword: string, signal?: AbortSignal): Promise<void>;
    requestPasswordReset(email: string, signal?: AbortSignal): Promise<void>;
    requestOTP(email: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<{
        otpId: string;
        expiresAt: string;
    }>>;
    completeOTP(otpId: string, code: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>>;
    confirmPasswordReset(token: string, newPassword: string, signal?: AbortSignal): Promise<void>;
    requestVerification(signal?: AbortSignal): Promise<void>;
    confirmVerification(token: string, signal?: AbortSignal): Promise<void>;
    requestEmailChange(currentPassword: string, newEmail: string, signal?: AbortSignal): Promise<void>;
    confirmEmailChange(token: string, signal?: AbortSignal): Promise<void>;
    records<T extends RecordData = RecordData>(collection: string): Records<T>;
    /**
     * Constructs a PocketBase filter expression without interpolating values.
     *
     * Parameters use PocketBase's `{:name}` placeholders.  Strings, dates,
     * arrays, and objects are emitted as quoted filter literals; scalar boolean,
     * number, and null values retain their filter-literal form.  A placeholder
     * without a matching parameter is deliberately left unchanged, matching the
     * PocketBase SDK and making an incomplete expression visible to the server.
     */
    filter(expression: string, params?: FilterParams): string;
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
    completeMFA(challengeToken: string, code: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>>;
    beginTOTPEnrollment(currentPassword: string, signal?: AbortSignal): Promise<{
        secret: string;
        otpAuthUrl: string;
    }>;
    confirmTOTPEnrollment(code: string, signal?: AbortSignal): Promise<{
        codes: string[];
    }>;
    disableTOTP(currentPassword: string, code: string, signal?: AbortSignal): Promise<void>;
    iterate<T extends RecordData>(collection: string, query?: Query, signal?: AbortSignal): AsyncGenerator<T>;
    _request<T>(path: string, init?: RequestInit, signal?: AbortSignal, requestKey?: string | null): Promise<T>;
    /** Cancels an in-flight request started with this key. */
    cancelRequest(requestKey: string): void;
    /** Cancels every keyed in-flight request. */
    cancelAllRequests(): void;
    /** Low-level request; null disables cancellation and a string supplies an explicit key. */
    send<T>(path: string, init?: RequestInit, options?: RequestOptions): Promise<T>;
    _response<T>(path: string, init?: RequestInit, signal?: AbortSignal, requestKey?: string | null): Promise<{
        body: T;
        etag?: string;
    }>;
    _responseText(path: string, init?: RequestInit, signal?: AbortSignal, requestKey?: string | null): Promise<{
        body: string;
        etag?: string;
    }>;
    private readResponse;
    _eventStream(path: string, lastEventId: string | undefined, signal?: AbortSignal): Promise<Response>;
    private authenticate;
    private request;
    private fetchResponse;
    private keyedSignal;
}
export declare class AuthRealm {
    private readonly client;
    readonly name: string;
    constructor(client: PgBaseClient, name: string);
    private path;
    private authenticate;
    register<T extends RecordData = RecordData>(email: string, password: string, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<UserSession>;
    login(email: string, password: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<UserSession>;
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
    oauthStart(provider: string, redirectUri: string, signal?: AbortSignal): Promise<OAuthStart>;
    /** Starts OAuth and delegates browser/popup/deep-link navigation to the host. */
    oauthStartAndOpen(provider: string, redirectUri: string, options: OAuthLaunchOptions, signal?: AbortSignal): Promise<{
        authorizationUrl: string;
        expiresAt: string;
    }>;
    oauthWithBrowser<T extends RecordData = RecordData>(provider: string, redirectUri: string, adapter: OAuthBrowserAdapter, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<UserSession>;
    oauthLink(currentPassword: string, provider: string, redirectUri: string, signal?: AbortSignal): Promise<OAuthStart>;
    oauthComplete<T extends RecordData = RecordData>(provider: string, state: string, code: string, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>>;
    oauthCompleteFromURL<T extends RecordData = RecordData>(provider: string, callbackURL: string | URL, options?: AuthCreationOptions<T> | AbortSignal, signal?: AbortSignal): Promise<Partial<UserSession>>;
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
    /** Returns the complete { items, nextCursor? } JSON envelope without parsing numbers. */
    listJSON(query?: Query, signal?: AbortSignal): Promise<string>;
    /**
     * Lists a numbered PocketBase-compatible page. This is intentionally
     * separate from [list], whose cursor contract remains native PgBase.
     */
    getList(page?: number, perPage?: number, options?: PocketBaseListOptions, signal?: AbortSignal): Promise<PocketBasePage<T>>;
    get(id: string, signal?: AbortSignal): Promise<T>;
    get(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<T>;
    getWithETag(id: string, signal?: AbortSignal): Promise<RecordResult<T>>;
    getWithETag(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<RecordResult<T>>;
    /** Returns the complete record JSON and ETag without parsing numbers. */
    getJSON(id: string, signal?: AbortSignal): Promise<RecordJSONResult>;
    getJSON(id: string, options?: RecordGetOptions, signal?: AbortSignal): Promise<RecordJSONResult>;
    create(data: RecordMutation<T>, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T>;
    createWithETag(data: RecordMutation<T>, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>>;
    /** Sends a JSON object verbatim; retry an uncertain create with the same body and key. */
    createJSON(recordJSON: string, idempotencyKey: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordJSONResult>;
    update(id: string, data: Partial<T> & RecordModifiers, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<T>;
    updateWithETag(id: string, data: Partial<T> & RecordModifiers, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordResult<T>>;
    /** Sends a record update JSON object verbatim with the current record ETag. */
    updateJSON(id: string, recordJSON: string, etag: string, options?: RecordWriteOptions | AbortSignal, signal?: AbortSignal): Promise<RecordJSONResult>;
    remove(id: string, etag: string, signal?: AbortSignal): Promise<void>;
    events(options?: EventOptions): AsyncGenerator<RecordEvent>;
    private listPath;
    private path;
    private eventsPath;
    private recordResponse;
    private recordJSONResponse;
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
