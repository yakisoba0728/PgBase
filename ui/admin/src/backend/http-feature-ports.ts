import type { AdminPortsTransport } from "./http-admin-ports";
import type { AuthCollectionsPort, AuthRecord, AuthRecordInput, AuthTokenPolicy, OAuthProviderSettings, RealtimePort, RealtimeStatus } from "./ports";

const realmPath = (id: string) => `/api/admin/auth-collections/${encodeURIComponent(id)}`;
export function createAuthFeaturePorts(t: AdminPortsTransport): Pick<AuthCollectionsPort, "records" | "tokenPolicies" | "putTokenPolicy" | "providerCatalog"> {
  const recordPath = (realm: string, id?: string) => realmPath(realm) + "/records" + (id === undefined ? "" : `/${encodeURIComponent(id)}`);
  const body = (input: AuthRecordInput, create: boolean) => ({ ...(create && input.id ? { id: input.id } : {}), email: input.email, ...(input.password ? { password: input.password } : {}), emailVisibility: input.emailVisibility, verified: input.verified, active: input.active, fields: input.fields });
  return {
    records: {
      list: async (realm, limit = 50, signal) => ((await t.schemaCall({ path: `${recordPath(realm)}?limit=${limit}`, signal })).data as { items: AuthRecord[] }).items,
      get: async (realm, id, signal) => (await t.schemaCall({ path: recordPath(realm, id), signal })).data as AuthRecord,
      create: async (realm, input, signal) => (await t.schemaCall({ path: recordPath(realm), method: "POST", body: body(input, true), csrf: true, signal })).data as AuthRecord,
      update: async (realm, id, input, signal) => (await t.schemaCall({ path: recordPath(realm, id), method: "PATCH", body: body(input, false), csrf: true, signal })).data as AuthRecord,
      remove: async (realm, id, signal) => { await t.schemaCall({ path: recordPath(realm, id), method: "DELETE", csrf: true, signal }); },
    },
    tokenPolicies: async (id, signal) => ((await t.schemaCall({ path: realmPath(id) + "/token-policies", signal })).data as { items: AuthTokenPolicy[] }).items,
    putTokenPolicy: async (id, purpose, value, signal) => (await t.schemaCall({ path: realmPath(id) + `/token-policies/${encodeURIComponent(purpose)}`, method: "PUT", body: value, csrf: true, signal })).data as AuthTokenPolicy,
    providerCatalog: async signal => ((await t.schemaCall({ path: "/api/admin/auth-collections/meta/oauth2-providers", signal })).data as { items: Array<Partial<OAuthProviderSettings> & Pick<OAuthProviderSettings, "name">> }).items,
  };
}
export function createRealtimePort(t: AdminPortsTransport): RealtimePort {
  return { status: async signal => (await t.send({ path: "/api/admin/realtime/status", signal })).data as RealtimeStatus,
    retry: async (id, signal) => { await t.send({ path: `/api/admin/realtime/${encodeURIComponent(id)}/retry`, method: "POST", csrf: true, signal }); } };
}
