import { request } from "./http";
import type { Administrator } from "./session";

// ---- Administrators ----

export interface AdministratorPage {
  items: Administrator[];
  /** Present when another page exists; pass it back as `after`. */
  nextAfter?: string;
}

/** limit is 1..100 (server default 50); after is the previous page's nextAfter. */
export function listAdministrators(
  params: { limit?: number; after?: string } = {},
  signal?: AbortSignal,
): Promise<AdministratorPage> {
  return request("GET", "/api/admin/administrators", { query: params, signal });
}

export function createAdministrator(input: { email: string; password: string }): Promise<Administrator> {
  return request("POST", "/api/admin/administrators", { body: input });
}

export function setAdministratorActive(id: string, active: boolean): Promise<Administrator> {
  return request("PATCH", `/api/admin/administrators/${encodeURIComponent(id)}`, { body: { active } });
}

export function resetAdministratorPassword(id: string, newPassword: string): Promise<void> {
  return request("POST", `/api/admin/administrators/${encodeURIComponent(id)}/password-reset`, { body: { newPassword } });
}

export function revokeAdministratorSessions(id: string): Promise<void> {
  // The handler strictly decodes an empty JSON object.
  return request("POST", `/api/admin/administrators/${encodeURIComponent(id)}/revoke-sessions`, { body: {} });
}

// ---- Status and health ----

export interface SetupStatus {
  setupRequired: boolean;
}

/** Public; tells the UI whether first-run setup is still pending. */
export function getSetupStatus(signal?: AbortSignal): Promise<SetupStatus> {
  return request("GET", "/api/admin/status", { signal });
}

export interface SystemStatusProbe {
  component: string;
  code: string;
}

export interface SystemStatus {
  version: string;
  databaseReachable: boolean;
  migrationCurrent: number;
  migrationLatest: number;
  migrationDirty: boolean;
  catalogVersion: number;
  listenerReady: boolean;
  unavailable: SystemStatusProbe[];
  sampledAt: string;
  lastSuccessAt: string;
}

export function getSystemStatus(signal?: AbortSignal): Promise<SystemStatus> {
  return request("GET", "/api/admin/system", { signal });
}

export interface SchemaFinding {
  severity: string;
  code: string;
  message: string;
}

export interface SchemaHealth {
  state: "healthy" | "drift" | "unavailable";
  findings: SchemaFinding[];
  /** Total findings of the run; `findings` may be a capped prefix. */
  findingCount: number;
  unavailable: SystemStatusProbe[];
  sampledAt: string;
  lastSuccessAt: string;
  retries: number;
}

export function getSchemaHealth(signal?: AbortSignal): Promise<SchemaHealth> {
  return request("GET", "/api/admin/system/health", { signal });
}

// ---- Runtime settings ----

export interface RuntimeSettings {
  batch: { enabled: boolean; maxRequests: number; maxBodyBytes: number; timeoutMs: number };
  logs: { retentionDays: number; level: string; includeIP: boolean; includeAuth: boolean };
  // Go nil slices are serialized as null, and the defaults leave these unset.
  proxy: { trustedProxyCIDRs: string[] | null };
  backups: { schedule: string; timezone: string; enabled: boolean; keep: number };
  application: { theme: string; accent: string; branding: string };
  rateLimits: {
    admin: { accountMaxFailures: number; networkMaxFailures: number; windowSeconds: number; blockSeconds: number };
    user: {
      accountMaxFailures: number;
      networkMaxFailures: number;
      registrationNetworkMaxFailures: number;
      windowSeconds: number;
      blockSeconds: number;
    };
    enabled: boolean;
    rules: { label: string; audience: string; duration: number; maxRequests: number }[] | null;
    excludedIPs: string[] | null;
  };
  revision: number;
}

/** `revision` is the optimistic-concurrency precondition; each supplied
 * section is merged key-by-key into the current value (409 on a stale revision). */
export type RuntimeSettingsPatch = { revision: number } & {
  [K in Exclude<keyof RuntimeSettings, "revision">]?: Partial<RuntimeSettings[K]>;
};

export function getSettings(signal?: AbortSignal): Promise<RuntimeSettings> {
  return request("GET", "/api/admin/settings", { signal });
}

export function updateSettings(patch: RuntimeSettingsPatch): Promise<RuntimeSettings> {
  return request("PATCH", "/api/admin/settings", { body: patch });
}

export function reloadSettings(): Promise<RuntimeSettings> {
  return request("POST", "/api/admin/settings/reload");
}

export interface AppleClientSecretInput {
  clientId: string;
  teamId: string;
  keyId: string;
  privateKey: string;
  /** Lifetime in seconds. */
  duration: number;
}

export function generateAppleClientSecret(input: AppleClientSecretInput): Promise<{ secret: string }> {
  return request("POST", "/api/admin/settings/apple/generate-client-secret", { body: input });
}

// ---- Service tokens ----

export interface ServiceTokenScope {
  collectionId: string;
  action: "read" | "create" | "update" | "delete";
}

export interface ServiceToken {
  id: string;
  name: string;
  active: boolean;
  expiresAt: string;
  revokedAt?: string;
  createdAt: string;
  scopes: ServiceTokenScope[];
}

/** Returned only once, at issue time: `token` is the raw secret. */
export interface IssuedServiceToken extends ServiceToken {
  token: string;
}

export interface IssueServiceTokenInput {
  name: string;
  /** 1 second .. 365 days. */
  ttlSeconds: number;
  scopes: ServiceTokenScope[];
}

export function listServiceTokens(signal?: AbortSignal): Promise<{ items: ServiceToken[] }> {
  return request("GET", "/api/admin/service-tokens", { signal });
}

export function issueServiceToken(input: IssueServiceTokenInput): Promise<IssuedServiceToken> {
  return request("POST", "/api/admin/service-tokens", { body: input });
}

export function revokeServiceToken(id: string): Promise<void> {
  return request("POST", `/api/admin/service-tokens/${encodeURIComponent(id)}/revoke`);
}
