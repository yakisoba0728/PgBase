import { request } from "./http";

// Contracts mirror internal/api/user_management.go and auth_admin.go.
// {id} is the internal user UUID across all auth collections (not an auth record's public id).

const base = "/api/admin/users";
const seg = encodeURIComponent;

export interface ManagedUser {
  id: string;
  email: string;
  verified: boolean;
  active: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface ManagedUserPage {
  items: ManagedUser[];
  /** Present when more users follow; pass back as `after`. */
  nextAfter?: string;
}

/** limit 1..100, default 50. */
export function listUsers(options: { after?: string; limit?: number; signal?: AbortSignal } = {}): Promise<ManagedUserPage> {
  return request("GET", base, { query: { after: options.after, limit: options.limit }, signal: options.signal });
}

export function setUserActive(id: string, active: boolean): Promise<ManagedUser> {
  return request("PATCH", `${base}/${seg(id)}`, { body: { active } });
}

export function setUserVerified(id: string, verified: boolean): Promise<ManagedUser> {
  return request("PUT", `${base}/${seg(id)}/verification`, { body: { verified } });
}

export async function revokeUserSessions(id: string): Promise<number> {
  return (await request<{ revokedSessions: number }>("POST", `${base}/${seg(id)}/revoke-sessions`)).revokedSessions;
}

/** 204 on success. */
export function sendUserVerification(id: string): Promise<void> {
  return request("POST", `${base}/${seg(id)}/send-verification`);
}

/** 204 on success. */
export function sendUserPasswordReset(id: string): Promise<void> {
  return request("POST", `${base}/${seg(id)}/send-password-reset`);
}

export interface ImpersonationSession {
  /** Secret bearer token for the impersonated user. */
  token?: string;
  user: {
    id: string;
    email: string;
    emailVisibility: boolean;
    verified: boolean;
    createdAt: string;
  };
  expiresAt: string;
  impersonated?: boolean;
  impersonatingAdministratorId?: string;
}

/** durationSeconds 60..900, server default 300. */
export function impersonateUser(id: string, durationSeconds?: number): Promise<ImpersonationSession> {
  return request("POST", `${base}/${seg(id)}/impersonate`, { body: durationSeconds === undefined ? {} : { durationSeconds } });
}

export interface OAuthIdentity {
  provider: string;
  subject: string;
  createdAt: string;
}

export async function listUserOAuthIdentities(id: string, signal?: AbortSignal): Promise<OAuthIdentity[]> {
  return (await request<{ items: OAuthIdentity[] | null }>("GET", `${base}/${seg(id)}/oauth-identities`, { signal })).items ?? [];
}

export function unlinkUserOAuthIdentity(id: string, provider: string): Promise<void> {
  return request("DELETE", `${base}/${seg(id)}/oauth-identities/${seg(provider)}`);
}
