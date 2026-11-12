import { request } from "./http";

// Contracts mirror internal/api/auth_collections.go, auth_admin.go and auth_records.go.

const base = "/api/admin/auth-collections";
const seg = encodeURIComponent;

// ---- Auth collections ----

/** `null` means "nobody" (rule locked); "" means everyone. */
export type AuthRule = string | null;

export interface AuthCollectionRules {
  list: AuthRule;
  view: AuthRule;
  create: AuthRule;
  update: AuthRule;
  delete: AuthRule;
  auth: AuthRule;
  manage: AuthRule;
}

export type AuthFieldType =
  | "boolean" | "integer" | "bigint" | "numeric" | "text" | "editor" | "geoPoint"
  | "autodate" | "password" | "timestamptz" | "uuid" | "jsonb" | "relation" | "file";

/** schema.FieldInput as used by auth collections. relation/options are left loose here. */
export interface AuthCollectionField {
  id?: string;
  name: string;
  type: AuthFieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  default?: unknown;
  relation?: Record<string, unknown>;
  options?: Record<string, unknown>;
  hidden?: boolean;
  presentable?: boolean;
  help?: string;
  /** Response-only in practice: set on the server-injected auth system fields. */
  system?: boolean;
}

export interface RecordIDPolicy {
  kind: "uuid" | "text";
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  autogeneratePattern?: string;
}

export interface AuthCollection {
  id: string;
  name: string;
  type: "auth";
  registrationEnabled: boolean;
  profileSchema: Record<string, string> | null;
  /** Auth system fields first, then the collection's own fields. */
  fields: AuthCollectionField[];
  rules: AuthCollectionRules;
  recordId?: RecordIDPolicy;
}

export interface CreateAuthCollectionInput {
  /** Client-generated UUID, required. */
  id: string;
  name: string;
  registrationEnabled: boolean;
  profileSchema?: Record<string, string>;
  fields?: AuthCollectionField[];
  rules?: Partial<AuthCollectionRules>;
  recordId?: RecordIDPolicy;
}

/** registrationEnabled is required, and at least one of profileSchema / fields. Omitted rules keep current rules. */
export interface UpdateAuthCollectionInput {
  registrationEnabled: boolean;
  profileSchema?: Record<string, string>;
  fields?: AuthCollectionField[];
  rules?: Partial<AuthCollectionRules>;
  recordId?: RecordIDPolicy;
}

export async function listAuthCollections(signal?: AbortSignal): Promise<AuthCollection[]> {
  return (await request<{ items: AuthCollection[] }>("GET", base, { signal })).items;
}

export function updateAuthCollection(id: string, input: UpdateAuthCollectionInput): Promise<AuthCollection> {
  return request("PATCH", `${base}/${seg(id)}`, { body: input });
}

export function deleteAuthCollection(id: string): Promise<void> {
  return request("DELETE", `${base}/${seg(id)}`);
}

// ---- OAuth provider catalog ----

export interface OAuthProviderCatalogEntry {
  name: "google" | "github" | "microsoft";
  authorizationUrl: string;
  tokenUrl: string;
  userInfoUrl: string;
  scopes: string[];
}

export async function listOAuthProviderCatalog(signal?: AbortSignal): Promise<OAuthProviderCatalogEntry[]> {
  return (await request<{ items: OAuthProviderCatalogEntry[] }>("GET", `${base}/meta/oauth2-providers`, { signal })).items;
}

// ---- Administration (policy, providers, templates, SMTP) ----

export interface AuthPolicy {
  passwordEnabled: boolean;
  minimumPasswordBytes: number;
  maximumPasswordBytes: number;
  passwordPattern: string;
  identityFields: string[];
  mfaEnabled: boolean;
  mfaDurationSeconds: number;
  mfaRule: string;
  otpEnabled: boolean;
  otpDurationSeconds: number;
  otpMaxAttempts: number;
}

/**
 * passwordEnabled, minimumPasswordBytes and mfaEnabled are required; other fields keep their
 * current value when omitted. The three otp* fields apply only when all three are sent.
 */
export type AuthPolicyInput =
  Pick<AuthPolicy, "passwordEnabled" | "minimumPasswordBytes" | "mfaEnabled"> &
  Partial<Omit<AuthPolicy, "passwordEnabled" | "minimumPasswordBytes" | "mfaEnabled">>;

/** Field names shared by the provider request and response. Provider name: [a-z0-9_-]{1,64}. */
interface OAuthProviderFields {
  enabled: boolean;
  authorizationUrl: string;
  tokenUrl: string;
  userInfoUrl: string;
  issuer: string;
  jwksUrl: string;
  clientId: string;
  scopes: string[];
  redirectUris: string[];
  subjectField: string;
  emailField: string;
  emailVerifiedField: string;
  avatarField: string;
  allowInsecureLoopback: boolean;
}

export interface OAuthProviderConfig extends OAuthProviderFields {
  /** Response-only. */
  name: string;
  /** Response-only: whether a client secret is stored. The secret itself is never returned. */
  secretConfigured: boolean;
}

/** All fields are decoded as given (omitted = zero value), except an empty clientSecret keeps the stored one. */
export interface OAuthProviderInput extends Partial<OAuthProviderFields> {
  /** Write-only secret. */
  clientSecret?: string;
}

export type MailTemplatePurpose = "verify" | "password_reset" | "email_change" | "otp" | "auth_alert";

/**
 * body must contain the purpose placeholder ({{action_url}}, or {{otp}} for otp, {{alert}} for
 * auth_alert); htmlBody too when non-empty. {{otp_id}} is also allowed.
 */
export interface MailTemplateInput {
  subject: string;
  body: string;
  htmlBody?: string;
}

export interface MailTemplate extends Required<MailTemplateInput> {
  purpose: MailTemplatePurpose;
}

export interface MailTemplatePreviewInput extends MailTemplateInput {
  actionUrl: string;
}

export interface MailPreview {
  subject: string;
  text: string;
  html: string;
}

interface SMTPFields {
  address: string;
  from: string;
  username: string;
  serverName: string;
  implicitTls: boolean;
  allowInsecureLoopback: boolean;
}

export interface SMTPConfig extends SMTPFields {
  /** Response-only: whether a password is stored. The password itself is never returned. */
  passwordConfigured: boolean;
}

export interface SMTPConfigInput extends Partial<SMTPFields> {
  /** Write-only secret; empty keeps the stored password. */
  password?: string;
}

export interface AuthAdministration {
  policy: AuthPolicy;
  providers: OAuthProviderConfig[] | null;
  templates: MailTemplate[] | null;
  smtp?: SMTPConfig;
}

export function getAuthAdministration(id: string, signal?: AbortSignal): Promise<AuthAdministration> {
  return request("GET", `${base}/${seg(id)}/administration`, { signal });
}

export function setAuthPolicy(id: string, input: AuthPolicyInput): Promise<AuthPolicy> {
  return request("PUT", `${base}/${seg(id)}/policy`, { body: input });
}

export function putOAuthProvider(id: string, provider: string, input: OAuthProviderInput): Promise<OAuthProviderConfig> {
  return request("PUT", `${base}/${seg(id)}/oauth-providers/${seg(provider)}`, { body: input });
}

export function deleteOAuthProvider(id: string, provider: string): Promise<void> {
  return request("DELETE", `${base}/${seg(id)}/oauth-providers/${seg(provider)}`);
}

export function putMailTemplate(id: string, purpose: MailTemplatePurpose, input: MailTemplateInput): Promise<MailTemplate> {
  return request("PUT", `${base}/${seg(id)}/mail-templates/${purpose}`, { body: input });
}

export function previewMailTemplate(id: string, purpose: MailTemplatePurpose, input: MailTemplatePreviewInput): Promise<MailPreview> {
  return request("POST", `${base}/${seg(id)}/mail-templates/${purpose}/preview`, { body: input });
}

export function putSMTPConfig(id: string, input: SMTPConfigInput): Promise<SMTPConfig> {
  return request("PUT", `${base}/${seg(id)}/smtp`, { body: input });
}

/** 204 on success. recipient must be a bare address. */
export function testSMTP(id: string, recipient: string): Promise<void> {
  return request("POST", `${base}/${seg(id)}/smtp/test`, { body: { recipient } });
}

// ---- Token policies ----

export type TokenPolicyPurpose = "verify" | "password_reset" | "email_change" | "session" | "otp" | "impersonation";

export interface TokenPolicy {
  purpose: TokenPolicyPurpose;
  ttlSeconds: number;
  keyVersion: string;
  /** Response-only and only meaningful on PUT: the list endpoint always reports false. */
  keyConfigured: boolean;
}

/** key and keyVersion rotate together or are both omitted (TTL-only update; a new purpose then gets a random key). */
export interface TokenPolicyInput {
  ttlSeconds: number;
  /** [A-Za-z0-9_-]{1,32} */
  keyVersion?: string;
  /** Write-only secret, at least 32 bytes. */
  key?: string;
}

export async function listTokenPolicies(id: string, signal?: AbortSignal): Promise<TokenPolicy[]> {
  return (await request<{ items: TokenPolicy[] | null }>("GET", `${base}/${seg(id)}/token-policies`, { signal })).items ?? [];
}

export function putTokenPolicy(id: string, purpose: TokenPolicyPurpose, input: TokenPolicyInput): Promise<TokenPolicy> {
  return request("PUT", `${base}/${seg(id)}/token-policies/${purpose}`, { body: input });
}

// ---- Auth records ----

export interface AuthRecord {
  /** Public record id (follows the collection's recordId policy). */
  id: string;
  authCollectionId: string;
  email?: string;
  emailVisibility: boolean;
  verified: boolean;
  active: boolean;
  fields: Record<string, unknown> | null;
  createdAt: string;
  updatedAt: string;
}

export interface CreateAuthRecordInput {
  id?: string;
  email: string;
  emailVisibility?: boolean;
  /** Write-only secret. */
  password: string;
  verified?: boolean;
  /** Defaults to true. */
  active?: boolean;
  fields?: Record<string, unknown>;
}

export interface UpdateAuthRecordInput {
  email?: string;
  emailVisibility?: boolean;
  /** Write-only secret. */
  password?: string;
  verified?: boolean;
  active?: boolean;
  fields?: Record<string, unknown>;
}

export async function listAuthRecords(id: string, options: { limit?: number; signal?: AbortSignal } = {}): Promise<AuthRecord[]> {
  const result = await request<{ items: AuthRecord[] | null }>("GET", `${base}/${seg(id)}/records`, { query: { limit: options.limit }, signal: options.signal });
  return result.items ?? [];
}

export function getAuthRecord(id: string, record: string, signal?: AbortSignal): Promise<AuthRecord> {
  return request("GET", `${base}/${seg(id)}/records/${seg(record)}`, { signal });
}

export function createAuthRecord(id: string, input: CreateAuthRecordInput): Promise<AuthRecord> {
  return request("POST", `${base}/${seg(id)}/records`, { body: input });
}

export function updateAuthRecord(id: string, record: string, input: UpdateAuthRecordInput): Promise<AuthRecord> {
  return request("PATCH", `${base}/${seg(id)}/records/${seg(record)}`, { body: input });
}

export function deleteAuthRecord(id: string, record: string): Promise<void> {
  return request("DELETE", `${base}/${seg(id)}/records/${seg(record)}`);
}
