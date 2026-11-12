import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { httpBackend } from "./http-backend";
import { wireResponse } from "../test/http-harness";
import { activeSessionExpiresAt, administrator } from "../test/harness";
import { parseJSON, stringifyJSON } from "./json";
import { routeAvailable } from "./capabilities";
const realm = "0199ab10-0000-7000-8000-000000000002";
const user = { id: "Case/ID", authCollectionId: realm, email: "member@example.test", active: true, verified: false, emailVisibility: false, fields: {}, createdAt: "2026-10-24T00:00:00Z", updatedAt: "2026-10-24T00:00:00Z" };
function transport(capabilities?: Record<string, boolean>) {
  const requests: Array<{ path: string; init?: RequestInit }> = [];
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    requests.push({ path, init });
    const body = path === "/api/admin/session" ? { administrator, expiresAt: activeSessionExpiresAt(), csrfToken: "feature-csrf", ...(capabilities ? { capabilities } : {}) } : path.includes("token-policies") ? path.endsWith("/session") ? { purpose: "session", ttlSeconds: 3600, keyVersion: "kept", keyConfigured: true } : { items: [] } : path.includes("realtime/status") ? { pending: 1, dead: 2, delivered: 3, cursorFloor: 4 } : path.includes("oauth2-providers") ? { items: [{ name: "github", authorizationUrl: "https://example.test/authorize" }] } : path.includes("/records?") ? { items: [user] } : user;
    return wireResponse({ status: 200, body: stringifyJSON(body), headers: { ETag: '"version"' } });
  });
  vi.stubGlobal("fetch", fetch);
  return { backend: httpBackend(), requests };
}
afterEach(() => vi.unstubAllGlobals());
beforeEach(() => { window.history.replaceState(null, "", "/admin/users"); });
it("preserves OIDC issuer and JWKS values through the actual provider PUT serializer", async () => {
  const { backend, requests } = transport(); await backend.session.resume();
  await backend.authCollections.putProvider(realm, { name: "oidc", enabled: true, authorizationUrl: "https://id.example.test/authorize", tokenUrl: "https://id.example.test/token", userInfoUrl: "", issuer: "https://id.example.test", jwksUrl: "https://id.example.test/jwks", clientId: "test", secretConfigured: true, scopes: ["openid"], redirectUris: [], subjectField: "sub", emailField: "email", emailVerifiedField: "email_verified", avatarField: "picture", allowInsecureLoopback: false });
  const call = requests.at(-1)!; expect(call.init?.method).toBe("PUT"); expect(parseJSON(call.init?.body as string)).toMatchObject({ issuer: "https://id.example.test", jwksUrl: "https://id.example.test/jwks", userInfoUrl: "" });
});
it("uses collection-specific auth CRUD paths and omits a blank replacement password", async () => {
  const { backend, requests } = transport(); await backend.session.resume(); const records = backend.authCollections.records!;
  await records.list(realm, 100); expect(requests.at(-1)?.path).toBe(`/api/admin/auth-collections/${realm}/records?limit=100`);
  await records.get(realm, user.id); expect(requests.at(-1)?.path).toContain("Case%2FID");
  await records.update(realm, user.id, { email: user.email, password: "", active: false, verified: true, emailVisibility: false, fields: { points: parseJSON("9223372036854775807") as never } });
  const call = requests.at(-1)!; expect(call.init?.method).toBe("PATCH"); expect(call.init?.body).toContain('"points":9223372036854775807'); expect(parseJSON(call.init?.body as string)).not.toHaveProperty("password"); expect(new Headers(call.init?.headers).get("X-CSRF-Token") || new Headers(call.init?.headers).get("X-PgBase-CSRF")).toBe("feature-csrf");
  await records.remove(realm, user.id); expect(requests.at(-1)?.init?.method).toBe("DELETE");
});
it("creates an auth record with its custom public ID and profile", async () => {
  const { backend, requests } = transport(); await backend.session.resume();
  await backend.authCollections.records!.create(realm, { id: "Case_ID", email: user.email, password: "synthetic test password", active: true, verified: false, emailVisibility: true, fields: { displayName: "Member" } });
  expect(parseJSON(requests.at(-1)!.init!.body as string)).toMatchObject({ id: "Case_ID", fields: { displayName: "Member" }, password: "synthetic test password" });
});
it("changes token duration without replacing the existing key, and sends a key only for explicit rotation", async () => {
  const { backend, requests } = transport(); await backend.session.resume();
  await backend.authCollections.putTokenPolicy!(realm, "session", { ttlSeconds: 3600 }); expect(parseJSON(requests.at(-1)!.init!.body as string)).toEqual({ ttlSeconds: 3600 });
  await backend.authCollections.putTokenPolicy!(realm, "session", { ttlSeconds: 3600, keyVersion: "v2", key: "a".repeat(32) }); expect(parseJSON(requests.at(-1)!.init!.body as string)).toMatchObject({ keyVersion: "v2", key: "a".repeat(32) });
});
it("connects realtime status/retry and the server provider catalog", async () => {
  const { backend, requests } = transport(); await backend.session.resume();
  expect(await backend.realtime!.status()).toEqual({ pending: 1, dead: 2, delivered: 3, cursorFloor: 4 });
  await backend.realtime!.retry("event/id"); expect(requests.at(-1)?.path).toBe("/api/admin/realtime/event%2Fid/retry"); expect(requests.at(-1)?.init?.method).toBe("POST");
  expect(await backend.authCollections.providerCatalog!()).toMatchObject([{ name: "github" }]);
});
it("honors explicit server capability flags while preserving compatibility with older sessions", async () => {
  const { backend } = transport({ settings: false, administrators: false, realtime: false, schemaTransfer: false, authCollections: false }); await backend.session.resume();
  expect(backend.settings).toBeUndefined(); expect(backend.realtime).toBeUndefined(); expect(routeAvailable(backend, { kind: "administrators" })).toBe(false); expect(routeAvailable(backend, { kind: "schema-transfer" })).toBe(false); expect(routeAvailable(backend, { kind: "auth-collections" })).toBe(false);
  const legacy = transport(); await legacy.backend.session.resume(); expect(routeAvailable(legacy.backend, { kind: "settings" })).toBe(true);
});
it("forwards advanced list options under the same cursor and keeps the normal default page size", async () => {
  const { backend, requests } = transport(); await backend.session.resume();
  await backend.records.list("articles", { cursor: "next", limit: 25, fields: "id,title", expand: "author", filter: "active = true", sort: "-created_at" });
  const query = new URL(requests.at(-1)!.path, "https://local.test").searchParams;
  expect(Object.fromEntries(query)).toEqual({ limit: "25", fields: "id,title", expand: "author", cursor: "next", filter: "active = true", sort: "-created_at" });
  await backend.records.list("articles", {}); expect(requests.at(-1)!.path).toContain("limit=50");
});
