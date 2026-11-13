import type {
  AuthCollection,
  AuthCollectionField,
  AuthPolicy,
  AuthPolicyInput,
  AuthRecord,
  MailTemplatePurpose,
  OAuthProviderCatalogEntry,
  OAuthProviderConfig,
  OAuthProviderInput,
} from "../api/authCollections";
import type { ManagedUser } from "../api/users";

// Client-side mirrors of the server validators in internal/userauth (auth_admin.go,
// oauth_http.go, token_policy.go): the API answers every rejection with a bare
// invalid_request, so field-level messages have to come from here.

/** Error codes; the UI maps each to an `authv.*` message. */
type Problem = "required" | "range" | "pattern" | "url" | "placeholder" | "braces" | "json" | "email" | "both" | "short" | "name" | "taken" | "rowRejected";
export type Problems = Record<string, Problem>;

const int = (text: string) => (/^\d+$/.test(text.trim()) ? Number(text) : NaN);
const inRange = (text: string, min: number, max: number) => {
  const value = int(text);
  return value >= min && value <= max;
};
const fieldName = /^[a-z][a-z0-9_]{0,62}$/;

// ---- Policy ----

export interface PolicyDraft {
  registrationEnabled: boolean;
  passwordEnabled: boolean;
  minimumPasswordBytes: string;
  maximumPasswordBytes: string;
  passwordPattern: string;
  identityFields: string;
  mfaEnabled: boolean;
  mfaDurationSeconds: string;
  mfaRule: string;
  otpEnabled: boolean;
  otpDurationSeconds: string;
  otpMaxAttempts: string;
}

export function policyDraft(policy: AuthPolicy, registrationEnabled: boolean): PolicyDraft {
  return {
    registrationEnabled,
    passwordEnabled: policy.passwordEnabled,
    minimumPasswordBytes: String(policy.minimumPasswordBytes),
    maximumPasswordBytes: String(policy.maximumPasswordBytes),
    passwordPattern: policy.passwordPattern,
    identityFields: (policy.identityFields ?? []).join(", "),
    mfaEnabled: policy.mfaEnabled,
    mfaDurationSeconds: String(policy.mfaDurationSeconds),
    mfaRule: policy.mfaRule,
    otpEnabled: policy.otpEnabled,
    otpDurationSeconds: String(policy.otpDurationSeconds),
    otpMaxAttempts: String(policy.otpMaxAttempts),
  };
}

const splitList = (text: string) => text.split(/[\s,]+/).filter(Boolean);

export function policyProblems(draft: PolicyDraft): Problems {
  const problems: Problems = {};
  if (!inRange(draft.minimumPasswordBytes, 12, 128)) problems.minimumPasswordBytes = "range";
  if (!inRange(draft.maximumPasswordBytes, Math.max(12, int(draft.minimumPasswordBytes) || 12), 4096)) problems.maximumPasswordBytes = "range";
  if (draft.passwordPattern.length > 512) problems.passwordPattern = "range";
  else if (draft.passwordPattern) {
    try {
      new RegExp(draft.passwordPattern);
    } catch {
      problems.passwordPattern = "pattern";
    }
  }
  const identities = splitList(draft.identityFields);
  if (identities.length === 0) problems.identityFields = "required";
  else if (identities.length > 8 || new Set(identities).size !== identities.length || identities.some((name) => name !== "email" && !fieldName.test(name))) problems.identityFields = "name";
  if (!inRange(draft.mfaDurationSeconds, 60, 86400)) problems.mfaDurationSeconds = "range";
  if (draft.mfaRule.trim()) {
    try {
      const rule = JSON.parse(draft.mfaRule) as { field?: unknown; equals?: unknown };
      if (typeof rule !== "object" || rule === null || typeof rule.field !== "string" || !("equals" in rule)) problems.mfaRule = "json";
    } catch {
      problems.mfaRule = "json";
    }
  }
  if (!inRange(draft.otpDurationSeconds, 60, 900)) problems.otpDurationSeconds = "range";
  if (!inRange(draft.otpMaxAttempts, 1, 10)) problems.otpMaxAttempts = "range";
  return problems;
}

/** The full PUT body: every field is sent so nothing silently keeps an older value. */
export function policyInput(draft: PolicyDraft): AuthPolicyInput {
  return {
    passwordEnabled: draft.passwordEnabled,
    minimumPasswordBytes: int(draft.minimumPasswordBytes),
    maximumPasswordBytes: int(draft.maximumPasswordBytes),
    passwordPattern: draft.passwordPattern,
    identityFields: splitList(draft.identityFields),
    mfaEnabled: draft.mfaEnabled,
    mfaDurationSeconds: int(draft.mfaDurationSeconds),
    mfaRule: draft.mfaRule.trim(),
    otpEnabled: draft.otpEnabled,
    otpDurationSeconds: int(draft.otpDurationSeconds),
    otpMaxAttempts: int(draft.otpMaxAttempts),
  };
}

// ---- OAuth providers ----

export interface ProviderDraft {
  name: string;
  enabled: boolean;
  clientId: string;
  /** Write-only: empty keeps the stored secret. */
  clientSecret: string;
  authorizationUrl: string;
  tokenUrl: string;
  userInfoUrl: string;
  issuer: string;
  jwksUrl: string;
  scopes: string;
  redirectUris: string;
  subjectField: string;
  emailField: string;
  emailVerifiedField: string;
  avatarField: string;
  allowInsecureLoopback: boolean;
}

export function providerDraft(source: OAuthProviderConfig | OAuthProviderCatalogEntry | { name: string }): ProviderDraft {
  const config = source as Partial<OAuthProviderConfig>;
  return {
    name: source.name,
    enabled: config.enabled ?? true,
    clientId: config.clientId ?? "",
    clientSecret: "",
    authorizationUrl: config.authorizationUrl ?? "",
    tokenUrl: config.tokenUrl ?? "",
    userInfoUrl: config.userInfoUrl ?? "",
    issuer: config.issuer ?? "",
    jwksUrl: config.jwksUrl ?? "",
    scopes: (config.scopes ?? []).join(" "),
    redirectUris: (config.redirectUris ?? []).join("\n"),
    subjectField: config.subjectField ?? "",
    emailField: config.emailField ?? "",
    emailVerifiedField: config.emailVerifiedField ?? "",
    avatarField: config.avatarField ?? "",
    allowInsecureLoopback: config.allowInsecureLoopback ?? false,
  };
}

/** https, or http on a loopback host when insecure loopback is allowed (oauth_http.go validEndpoint). */
export function validEndpoint(raw: string, allowLoopback: boolean): boolean {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return false;
  }
  if (!url.host || url.username || url.password || url.hash || raw.includes("#")) return false;
  if (url.protocol === "https:") return true;
  const host = url.hostname.replace(/^\[|\]$/g, "");
  return allowLoopback && url.protocol === "http:" && (host === "localhost" || host === "::1" || /^127\./.test(host));
}

export function providerProblems(draft: ProviderDraft): Problems {
  const problems: Problems = {};
  const loopback = draft.allowInsecureLoopback;
  if (!/^[a-z0-9_-]{1,64}$/.test(draft.name)) problems.name = "name";
  if (!draft.clientId.trim()) problems.clientId = "required";
  for (const key of ["authorizationUrl", "tokenUrl"] as const) {
    if (!draft[key]) problems[key] = "required";
    else if (!validEndpoint(draft[key], loopback)) problems[key] = "url";
  }
  if (draft.userInfoUrl && !validEndpoint(draft.userInfoUrl, loopback)) problems.userInfoUrl = "url";
  if (draft.jwksUrl && !validEndpoint(draft.jwksUrl, loopback)) problems.jwksUrl = "url";
  if (draft.jwksUrl && !validEndpoint(draft.issuer, loopback)) problems.issuer = draft.issuer ? "url" : "required";
  if (!draft.jwksUrl && !draft.userInfoUrl) problems.userInfoUrl = "both";
  const redirects = draft.redirectUris.split("\n").map((line) => line.trim()).filter(Boolean);
  if (redirects.length > 16 || redirects.some((uri) => !validEndpoint(uri, loopback))) problems.redirectUris = "url";
  if (splitList(draft.scopes).length > 32) problems.scopes = "range";
  return problems;
}

/** The whole form: omitted non-secret fields would be stored as zero values. */
export function providerInput(draft: ProviderDraft): OAuthProviderInput {
  const { name: _name, clientSecret, scopes, redirectUris, ...rest } = draft;
  return {
    ...rest,
    clientId: rest.clientId.trim(),
    scopes: splitList(scopes),
    redirectUris: redirectUris.split("\n").map((line) => line.trim()).filter(Boolean),
    ...(clientSecret ? { clientSecret } : {}),
  };
}

// ---- Mail templates ----

export const mailPurposes: MailTemplatePurpose[] = ["verify", "password_reset", "email_change", "otp", "auth_alert"];

export const placeholderFor = (purpose: MailTemplatePurpose) => (purpose === "otp" ? "{{otp}}" : purpose === "auth_alert" ? "{{alert}}" : "{{action_url}}");

export interface TemplateDraft {
  subject: string;
  body: string;
  htmlBody: string;
}

export function templateProblems(purpose: MailTemplatePurpose, draft: TemplateDraft): Problems {
  const problems: Problems = {};
  const placeholder = placeholderFor(purpose);
  if (!draft.subject) problems.subject = "required";
  else if (draft.subject.length > 200 || /[\r\n]/.test(draft.subject)) problems.subject = "range";
  if (!draft.body) problems.body = "required";
  else if (draft.body.length > 16384) problems.body = "range";
  else if (!draft.body.includes(placeholder)) problems.body = "placeholder";
  if (draft.htmlBody.length > 32768) problems.htmlBody = "range";
  else if (draft.htmlBody && !draft.htmlBody.includes(placeholder)) problems.htmlBody = "placeholder";
  for (const key of ["body", "htmlBody"] as const) {
    const rest = draft[key].replaceAll(placeholder, "").replaceAll("{{otp_id}}", "");
    if (!problems[key] && (rest.includes("{{") || rest.includes("}}"))) problems[key] = "braces";
  }
  return problems;
}

// ---- Token policies ----

export interface TokenDraft {
  /** Empty leaves an unconfigured purpose on the server default. */
  ttlSeconds: string;
  /** Rotation: both empty, or a new key (>= 32 bytes) with a new version. */
  key: string;
  keyVersion: string;
}

export function tokenProblems(draft: TokenDraft, configured: boolean): Problems {
  const problems: Problems = {};
  const rotating = Boolean(draft.key || draft.keyVersion);
  if (draft.ttlSeconds === "" ? configured || rotating : !inRange(draft.ttlSeconds, 60, 86400)) problems.ttlSeconds = draft.ttlSeconds ? "range" : "required";
  if (rotating) {
    if (new TextEncoder().encode(draft.key).length < 32 || draft.key.length > 4096) problems.key = draft.key ? "short" : "required";
    if (!/^[A-Za-z0-9_-]{1,32}$/.test(draft.keyVersion)) problems.keyVersion = draft.keyVersion ? "name" : "required";
  }
  return problems;
}

// ---- SMTP / misc ----

/** A bare address, as net/mail.ParseAddress accepts it without a display name. */
export const bareEmail = (value: string) => /^[^\s@<>()",;:]+@[^\s@<>()",;:]+$/.test(value);

// ---- Users ↔ auth records ----

/**
 * /api/admin/users lists internal user UUIDs without their collection; auth records carry the
 * public id (equal to the UUID under the uuid id policy, generated text otherwise). A record
 * matches by id, or by email plus creation time for text-id collections.
 */
export function matchesUser(record: AuthRecord, user: Pick<ManagedUser, "id" | "email" | "createdAt">): boolean {
  return record.id === user.id || (record.email === user.email && Date.parse(record.createdAt) === Date.parse(user.createdAt));
}

/** Profile fields an administrator edits: the collection's own, typed fields. */
export const profileFields = (collection: AuthCollection): AuthCollectionField[] =>
  collection.fields.filter((field) => !field.system && field.type !== "password" && field.type !== "autodate" && field.type !== "file");

export type ProfileDraft = Record<string, string | boolean>;

const jsonTyped = (type: string) => type === "jsonb" || type === "geoPoint" || type === "relation";

export function profileDraft(fields: AuthCollectionField[], values: Record<string, unknown> | null): ProfileDraft {
  const draft: ProfileDraft = {};
  for (const field of fields) {
    const value = values?.[field.name];
    if (field.type === "boolean") draft[field.name] = value === true;
    else if (value === undefined || value === null) draft[field.name] = "";
    else draft[field.name] = jsonTyped(field.type) ? JSON.stringify(value, null, 2) : String(value);
  }
  return draft;
}

/** Changed fields only (the server merges profile updates), plus per-field problems. */
export function profileChanges(fields: AuthCollectionField[], draft: ProfileDraft, initial: ProfileDraft): { changes: Record<string, unknown>; problems: Problems } {
  const changes: Record<string, unknown> = {};
  const problems: Problems = {};
  for (const field of fields) {
    const value = draft[field.name];
    if (typeof value === "boolean") {
      if (value !== initial[field.name]) changes[field.name] = value;
      continue;
    }
    const text = value.trim();
    if (text === "" && field.required) problems[field.name] = "required";
    if (value === initial[field.name]) continue;
    if (text === "") changes[field.name] = null;
    else if (jsonTyped(field.type)) {
      try {
        changes[field.name] = JSON.parse(text);
      } catch {
        problems[field.name] = "json";
      }
    } else if (field.type === "integer" || field.type === "bigint" || field.type === "numeric") {
      // shortcut: numbers go through JS Number; bigint values past 2^53 need a lossless input.
      const number = Number(text);
      if (!Number.isFinite(number) || ((field.type === "integer" || field.type === "bigint") && !Number.isInteger(number))) problems[field.name] = "range";
      else changes[field.name] = number;
    } else changes[field.name] = value;
  }
  return { changes, problems };
}

/**
 * The UUID just below `id`: the users list is `id > after` in id order, so `after` set to it
 * starts the page exactly at `id`. Undefined for the lowest UUID; null when `id` is not a UUID.
 */
export function uuidBefore(id: string): string | undefined | null {
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)) return null;
  const value = BigInt(`0x${id.replace(/-/g, "")}`);
  if (value === BigInt(0)) return undefined;
  const hex = (value - BigInt(1)).toString(16).padStart(32, "0");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
