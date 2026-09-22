export type User = { id: string; email: string; createdAt: string; verified?: boolean; emailVisibility?: boolean; authCollectionId?: string; authCollection?: string };
export type UserSession = { token?: string; user: User; expiresAt: string; impersonated?: boolean; impersonatingAdministratorId?: string };
export type GeoPoint = { lon: number; lat: number };
export type RecordData = Record<string, unknown>;
/** Values accepted by [PgBaseClient.filter] placeholder substitution. */
export type FilterParams = Record<string, unknown>;

export type RecordPage<T extends RecordData> = { items: T[]; nextCursor?: string };
/** Options for the PocketBase offset-pagination compatibility endpoint. */
export type PocketBaseListOptions = Pick<Query, "filter" | "sort" | "expand" | "fields" | "requestKey"> & { skipTotal?: boolean };
/** PocketBase's page-number list envelope. A skipped count is represented by -1. */
export type PocketBasePage<T extends RecordData> = { page: number; perPage: number; totalItems: number; totalPages: number; items: T[] };
/** Record input may include PocketBase mutation keys such as `score+`, `+tags`, and `tags-`. */
export type RecordModifiers = { [key: `${string}+`]: unknown; [key: `+${string}`]: unknown; [key: `${string}-`]: unknown };
/** An explicit ID is accepted when the collection uses text record identities. */
export type RecordMutation<T extends RecordData> = Omit<T, "id"> & { id?: string } & RecordModifiers;
export type RecordResult<T extends RecordData> = { record: T; etag?: string };
/** The complete record JSON response, left unparsed to preserve exact numbers. */
export type RecordJSONResult = { recordJSON: string; etag?: string };
export type Query = {
  filter?: string;
  sort?: string;
  expand?: string;
  /** Comma-separated record fields to return. */
  fields?: string;
  cursor?: string;
  limit?: number;
  /** A custom cancellation group; null keeps this request independent. Never sent to the server. */
  requestKey?: string | null;
};
/** Options accepted by a single-record read. */
export type RecordGetOptions = Pick<Query, "expand" | "fields" | "requestKey">;
/** Options accepted by record mutations. */
export type RecordWriteOptions = Pick<Query, "fields" | "requestKey">;
/** Custom signup fields go in the request body; `fields` remains response projection. */
export type AuthCreationOptions<T extends RecordData = RecordData> = RecordWriteOptions & { profile?: T };
export type BatchOptions = Pick<Query, "fields" | "requestKey">;
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
/** Reconnect policy for the shared PocketBase-compatible realtime session. */
export type RealtimeSubscriptionOptions = Pick<EventOptions, "maxReconnects" | "reconnectDelayMs">;
/** Shared-session lifecycle snapshot; listener failures leave the current state intact. */
export type RealtimeStatus = Readonly<{
  state: "connecting" | "connected" | "reconnecting" | "stopped";
  attempt: number;
  lastEventId?: string;
  reason?: "unsubscribed" | "terminal" | "exhausted" | "reset";
  error?: unknown;
  source?: "transport" | "listener";
  topic?: string;
}>;
/** A collection record event, retained for existing PocketBase subscriptions. */
export type RealtimeEvent<T extends RecordData = RecordData> = { action: "create" | "update" | "delete"; record: T; topic: string };
/** An application topic event. Its payload is arbitrary valid JSON. */
export type RealtimeCustomEvent = { topic: string; data: unknown };
export type RealtimeSubscriptionEvent<T extends RecordData = RecordData> = RealtimeEvent<T> | RealtimeCustomEvent;
export type RequestOptions = { signal?: AbortSignal; requestKey?: string | null };
/** Opens the provider URL using a browser popup, redirect, or mobile launcher. */
export type OAuthLaunchOptions = { open(url: string): void | Promise<void> };
export type OAuthBrowserAdapter = OAuthLaunchOptions & { waitForCallback(signal?: AbortSignal): Promise<string | URL>; close?(): void };
export type BatchOperation<T extends RecordData = RecordData> =
  | { operation: "create"; collection: string; input: RecordMutation<T>; idempotencyKey?: string }
  | { operation: "update"; collection: string; id: string; input: Partial<T>; ifMatch?: string }
  | BatchUpsertOperation<T>
  | { operation: "delete"; collection: string; id: string; ifMatch?: string };
/** An atomic PUT-equivalent batch operation: creates the ID when absent, otherwise updates it. */
export type BatchUpsertOperation<T extends RecordData = RecordData> = {
  operation: "upsert";
  collection: string;
  id: string;
  input: Partial<T>;
  ifMatch?: string;
};
export type BatchResult<T extends RecordData = RecordData> = { eventId: string; record?: T; etag?: string; version?: number };
export type BatchHTTPRequest<T extends RecordData = RecordData> = { method: "POST" | "PATCH" | "PUT" | "DELETE"; url: string; body?: Partial<T>; headers?: Record<string,string> };
export type BatchHTTPResult<T extends RecordData = RecordData> = { status: number; headers?: Record<string,string>; body?: T; eventId: string; etag?: string; version?: number };
export type BatchFilePart = { blob: Blob; filename: string };
export type FileInfo = { id: string; field: string; filename: string; contentType: string; size: number; thumbnail: boolean };
export type MFAChallenge = { challengeToken: string; expiresAt: string };
/** Enabled sign-in methods for one explicit public auth collection. */
export type AuthMethods = {
  passwordEnabled: boolean;
  otpEnabled: boolean;
  mfaEnabled: boolean;
  oauthProviders: string[];
  oauth2: { enabled: boolean; providers: Array<{ name: string; startEndpoint: string }> };
};
export type OAuthStart = { authorizationUrl: string; expiresAt: string; state: string; codeVerifier: string; codeChallenge: string; codeChallengeMethod: string };

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
