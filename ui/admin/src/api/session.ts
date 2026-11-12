import { ApiError, clearCsrfToken, request, setCsrfToken } from "./http";

// POST /api/admin/login is a legacy alias served by the same handler as
// /api/admin/auth/login, so it is intentionally not wrapped.

export interface Administrator {
  id: string;
  email: string;
  active: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface AdminSession {
  administrator: Administrator;
  csrfToken: string;
  expiresAt: string;
  sqlConsoleEnabled: boolean;
  /** Which optional admin surfaces this server exposes, keyed by feature name. */
  capabilities?: Record<string, boolean>;
}

/** Backup archive metadata returned by the setup upload. */
export interface SetupBackupEntry {
  id: string;
  createdAt: string;
  size: number;
  postgresqlMajor: number;
  sha256?: string;
  storageFormat?: string;
}

export interface SetupBackupJob {
  id: string;
  operation: string;
  backupId: string;
  /** "running" | "completed" | "failed" */
  status: string;
  phase: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

export interface SetupInput {
  setupToken: string;
  email: string;
  password: string;
}

export interface LoginInput {
  email: string;
  password: string;
  otpCode?: string;
}

export interface IpAllowlist {
  prefixes: string[];
}

async function adopt(session: Promise<AdminSession>): Promise<AdminSession> {
  const value = await session;
  setCsrfToken(value.csrfToken);
  return value;
}

export function setup(input: SetupInput): Promise<AdminSession> {
  return adopt(request<AdminSession>("POST", "/api/admin/setup", { body: input }));
}

const setupHeaders = (setupToken: string) => ({ "X-PgBase-Setup-Token": setupToken });

export function getSetupBackupJob(setupToken: string, id: string): Promise<SetupBackupJob> {
  return request("GET", `/api/admin/setup/backup-jobs/${encodeURIComponent(id)}`, { headers: setupHeaders(setupToken) });
}

/** The server accepts only application/gzip, whatever the file's own type. */
export function uploadSetupBackup(setupToken: string, archive: Blob, signal?: AbortSignal): Promise<SetupBackupEntry> {
  return request("POST", "/api/admin/setup/backups/upload", {
    raw: archive,
    signal,
    headers: { ...setupHeaders(setupToken), "Content-Type": "application/gzip" },
  });
}

export function restoreSetupBackup(setupToken: string, id: string): Promise<SetupBackupJob> {
  return request("POST", `/api/admin/setup/backups/${encodeURIComponent(id)}/restore`, { headers: setupHeaders(setupToken) });
}

export function login(input: LoginInput): Promise<AdminSession> {
  return adopt(request<AdminSession>("POST", "/api/admin/auth/login", { body: input }));
}

export async function logout(keepalive = false): Promise<void> {
  try {
    await request<void>("POST", "/api/admin/auth/logout", { keepalive });
  } finally {
    clearCsrfToken();
  }
}

/** Restores the CSRF token for an existing cookie session; clears it when the session is gone. */
export async function getSession(signal?: AbortSignal): Promise<AdminSession> {
  try {
    return await adopt(request<AdminSession>("GET", "/api/admin/session", { signal }));
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) clearCsrfToken();
    throw error;
  }
}

export function requestPasswordReset(email: string): Promise<void> {
  return request("POST", "/api/admin/auth/request-password-reset", { body: { email } });
}

export function confirmPasswordReset(token: string, newPassword: string): Promise<void> {
  return request("POST", "/api/admin/auth/confirm-password-reset", { body: { token, newPassword } });
}

export function getProfile(signal?: AbortSignal): Promise<Administrator> {
  return request("GET", "/api/admin/profile", { signal });
}

export function updateProfile(email: string): Promise<Administrator> {
  return request("PATCH", "/api/admin/profile", { body: { email } });
}

export function getIpAllowlist(signal?: AbortSignal): Promise<IpAllowlist> {
  return request("GET", "/api/admin/profile/ip-allowlist", { signal });
}

export function updateIpAllowlist(prefixes: string[]): Promise<IpAllowlist> {
  return request("PATCH", "/api/admin/profile/ip-allowlist", { body: { prefixes } });
}
