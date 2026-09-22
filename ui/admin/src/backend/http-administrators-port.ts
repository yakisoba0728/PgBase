import type { AdministratorPage, AdministratorProfile, AdministratorsPort } from "./ports";
import { AdministratorError } from "./ports";
import { WireError, type Reply, type Request } from "./http-transport";

export function createAdministratorsPort(transport: {
  send(request: Request): Promise<Reply>;
  authGeneration(): number;
  sessionsRevoked(id: string, generation: number): void;
}): AdministratorsPort {
  const base = "/api/admin/administrators";
  async function send(request: Request) {
    try { return await transport.send(request); }
    catch (caught) {
      const code = caught instanceof WireError ? caught.wire.code : "";
      throw new AdministratorError(code === "administrator_conflict" ? "conflict" : code === "invalid_administrator" ? "invalid" : code === "administrator_not_found" ? "not-found" : "failed");
    }
  }
  async function revoke(id: string, action: string, body: object, signal?: AbortSignal) {
    const generation = transport.authGeneration();
    await send({ path: `${base}/${encodeURIComponent(id)}/${action}`, method: "POST", body, csrf: true, signal });
    transport.sessionsRevoked(id, generation);
  }
  return {
    list: async (after, signal) => {
      const query = new URLSearchParams({ limit: "50" });
      if (after) query.set("after", after);
      return (await send({ path: `${base}?${query}`, signal })).data as AdministratorPage;
    },
    create: async (value, signal) => (await send({ path: base, method: "POST", body: { email: value.email, password: value.password }, csrf: true, signal })).data as AdministratorProfile,
    setActive: async (id, active, signal) => (await send({ path: `${base}/${encodeURIComponent(id)}`, method: "PATCH", body: { active }, csrf: true, signal })).data as AdministratorProfile,
    resetPassword: (id, newPassword, signal) => revoke(id, "password-reset", { newPassword }, signal),
    revokeSessions: (id, signal) => revoke(id, "revoke-sessions", {}, signal),
  };
}
