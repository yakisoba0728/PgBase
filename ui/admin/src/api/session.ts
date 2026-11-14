import { advanceSession, sessionGeneration, ApiError, clearCsrfToken, request, setCsrfToken, upload } from "./http";

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
  /** The address the server checks the list against, after trusted-proxy resolution. */
  clientAddress: string;
}

async function adopt(session: Promise<AdminSession>): Promise<AdminSession> {
  const generation = sessionGeneration();
  const value = await session;
  if (generation === sessionGeneration()) setCsrfToken(value.csrfToken);
  return value;
}

export function setup(input: SetupInput): Promise<AdminSession> {
  advanceSession();
  return adopt(request<AdminSession>("POST", "/api/admin/setup", { body: input }));
}

const setupHeaders = (setupToken: string) => ({ "X-PgBase-Setup-Token": setupToken });

export function getSetupBackupJob(setupToken: string, id: string): Promise<SetupBackupJob> {
  return request("GET", `/api/admin/setup/backup-jobs/${encodeURIComponent(id)}`, { headers: setupHeaders(setupToken) });
}

/** The server accepts only application/gzip, whatever the file's own type. */
export function uploadSetupBackup(setupToken: string, archive: Blob, signal?: AbortSignal, onProgress?: (fraction: number) => void): Promise<SetupBackupEntry> {
  return upload("/api/admin/setup/backups/upload", archive, {
    headers: { ...setupHeaders(setupToken), "Content-Type": "application/gzip" },
    signal,
    onProgress,
  });
}

export function restoreSetupBackup(setupToken: string, id: string): Promise<SetupBackupJob> {
  return request("POST", `/api/admin/setup/backups/${encodeURIComponent(id)}/restore`, { headers: setupHeaders(setupToken) });
}

export function login(input: LoginInput): Promise<AdminSession> {
  advanceSession();
  return adopt(request<AdminSession>("POST", "/api/admin/auth/login", { body: input }));
}

export async function logout(keepalive = false): Promise<void> {
  advanceSession();
  const generation = sessionGeneration();
  try {
    await request<void>("POST", "/api/admin/auth/logout", { keepalive });
  } finally {
    if (generation === sessionGeneration()) clearCsrfToken();
  }
}

/** Restores the CSRF token for an existing cookie session; clears it when the session is gone. */
export async function getSession(signal?: AbortSignal): Promise<AdminSession> {
  const generation = sessionGeneration();
  try {
    return await adopt(request<AdminSession>("GET", "/api/admin/session", { signal }));
  } catch (error) {
    if (generation === sessionGeneration() && error instanceof ApiError && error.status === 401) clearCsrfToken();
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

/** Changes your own sign-in email and/or password; the server re-checks the current password. A new password ends every session. */
export function updateProfile(input: { currentPassword: string; email?: string; newPassword?: string }): Promise<Administrator> {
  return request("PATCH", "/api/admin/profile", { body: input });
}

export function getIpAllowlist(signal?: AbortSignal): Promise<IpAllowlist> {
  return request("GET", "/api/admin/profile/ip-allowlist", { signal });
}

/** Replaces your allowlist; like every administrator-security change it needs your current password (403 `current_password_invalid`). */
export function updateIpAllowlist(prefixes: string[], currentPassword: string): Promise<IpAllowlist> {
  return request("PATCH", "/api/admin/profile/ip-allowlist", { body: { currentPassword, prefixes } });
}
