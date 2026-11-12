import type { Collection, CollectionField } from "../api/collections";
import type { Action } from "../rules/model";

// A per-collection reference built only from the catalog entry: routes follow
// docs/public-api.md, docs/realtime.md and docs/user-access.md.

export type Audience = "guest" | "user";
export type Method = "GET" | "POST" | "PATCH" | "DELETE";

export interface Endpoint {
  id: string;
  method: Method;
  path: string;
  /** The collection rule that gates it; absent for auth routes. */
  rule?: Action;
  /** Bearer session required whatever the audience (auth, files, realtime). */
  userOnly?: boolean;
  query?: Record<string, string>;
  headers?: Record<string, string>;
  body?: Record<string, unknown>;
  status: number;
  response?: unknown;
}

const exampleId = "01890a5d-ac96-7f1d-8b3f-0d7a1c2e4b5f";
const exampleTime = "2026-01-01T09:00:00Z";

export const recordIdExample = (collection: Collection) => (collection.recordId?.kind === "text" ? "abc123def456ghi" : exampleId);

/** A plausible value for a field, honouring choices, formats and bounds. */
export function exampleValue(field: CollectionField): unknown {
  const options = field.options ?? {};
  switch (field.type) {
    case "text":
      if (options.allowedValues?.length) return options.allowedValues[0];
      if (options.format === "email") return "person@example.com";
      if (options.format === "url") return "https://example.com";
      return "example";
    case "editor":
      return "<p>Hello</p>";
    case "integer":
    case "bigint":
      return options.min !== undefined ? Number(options.min) : 1;
    case "numeric":
      return options.min !== undefined ? Number(options.min) : 1.5;
    case "boolean":
      return true;
    case "timestamptz":
    case "autodate":
      return exampleTime;
    case "uuid":
      return exampleId;
    case "jsonb":
      return options.allowedValues?.length ? [options.allowedValues[0]] : { key: "value" };
    case "relation":
      return field.relation?.cardinality === "multiple" ? [exampleId] : exampleId;
    case "geoPoint":
      return { lon: 126.978, lat: 37.5665 };
    case "password":
      return "a-long-passphrase";
    case "file":
      return undefined;
  }
}

const writable = (field: CollectionField) => !field.system && field.type !== "autodate" && field.type !== "file";

export function requestBody(collection: Collection): Record<string, unknown> {
  return Object.fromEntries(collection.fields.filter(writable).map((field) => [field.name, exampleValue(field)]));
}

/** A record as returned: hidden and file fields are absent, passwords come back empty. */
export function responseRecord(collection: Collection): Record<string, unknown> {
  const record: Record<string, unknown> = { id: recordIdExample(collection) };
  for (const field of collection.fields) {
    if (field.hidden || field.type === "file" || field.name === "id") continue;
    record[field.name] = field.type === "password" ? "" : exampleValue(field);
  }
  return { ...record, created_at: exampleTime, updated_at: exampleTime };
}

/** `realm`: an auth collection of this name is registered, so its sign-in routes exist. */
export function buildEndpoints(collection: Collection, audience: Audience, realm = false): Endpoint[] {
  const name = encodeURIComponent(collection.name);
  const prefix = audience === "guest" ? "/api/v1/public" : "/api/v1/user";
  const records = `${prefix}/collections/${name}/records`;
  const one = `${records}/{id}`;
  const record = responseRecord(collection);
  const body = requestBody(collection);
  const firstText = collection.fields.find((field) => field.type === "text" && !field.hidden)?.name;
  const list: Endpoint = {
    id: "list",
    method: "GET",
    path: records,
    rule: "list",
    query: { ...(firstText ? { filter: `${firstText} ~ 'example'` } : {}), sort: "-created_at", limit: "20" },
    status: 200,
    response: { items: [record], nextCursor: "eyJ2IjoxfQ" },
  };
  const view: Endpoint = { id: "view", method: "GET", path: one, rule: "view", status: 200, response: record };
  const realtime: Endpoint = { id: "realtime", method: "GET", path: `/api/v1/user/collections/${name}/events`, rule: "list", userOnly: true, status: 200 };
  if (collection.type === "view") return [list, view, realtime];

  const endpoints: Endpoint[] = [
    list,
    view,
    { id: "create", method: "POST", path: records, rule: "create", headers: { "Idempotency-Key": "create-0000000001" }, body, status: 201, response: record },
    { id: "update", method: "PATCH", path: one, rule: "update", headers: { "If-Match": '"etag"' }, body, status: 200, response: record },
    { id: "delete", method: "DELETE", path: one, rule: "delete", headers: { "If-Match": '"etag"' }, status: 204 },
    {
      id: "batch",
      method: "POST",
      path: `${prefix}/collections/batch`,
      rule: "create",
      body: { operations: [{ operation: "create", collection: collection.name, input: body, idempotencyKey: "batch-create-000001" }] },
      status: 200,
      response: { results: [{ eventId: exampleId, record, etag: '"etag"', version: 1 }] },
    },
  ];
  for (const field of collection.fields.filter((item) => item.type === "file")) {
    endpoints.push({
      id: `files-${field.name}`,
      method: "POST",
      path: `/api/v1/user/collections/${name}/records/{id}/files/${encodeURIComponent(field.name)}`,
      rule: "update",
      userOnly: true,
      headers: { "X-PgBase-File-Size": "1024", "X-PgBase-File-SHA256": "<sha256 hex>" },
      status: 201,
    });
  }
  endpoints.push(realtime);
  if (collection.type === "auth" && realm) {
    const auth = `/api/v1/auth/${name}`;
    const session = { token: "<token>", user: { id: exampleId, email: "person@example.com", createdAt: exampleTime }, expiresAt: exampleTime };
    const credentials = { email: "person@example.com", password: "a-long-passphrase" };
    endpoints.push(
      { id: "authMethods", method: "GET", path: `${auth}/methods`, status: 200, response: { passwordEnabled: true, identityFields: ["email"], otpEnabled: false, mfaEnabled: false, oauthProviders: [], oauth2: { enabled: false, providers: [] } } },
      { id: "authRegister", method: "POST", path: `${auth}/register`, body: credentials, status: 201, response: session },
      { id: "authLogin", method: "POST", path: `${auth}/login`, body: credentials, status: 200, response: session },
      { id: "authRefresh", method: "POST", path: `${auth}/refresh`, userOnly: true, status: 200, response: session },
      { id: "authProfile", method: "GET", path: `${auth}/profile`, userOnly: true, status: 200, response: { attributes: body } },
      { id: "authReset", method: "POST", path: `${auth}/request-password-reset`, body: { email: credentials.email }, status: 204 },
    );
  }
  return endpoints;
}
