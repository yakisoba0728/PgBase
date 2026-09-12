import type {
  AuthenticatedSession,
  LoginInput,
  ResumedSession,
  SessionPort,
  SetupBackup,
  SetupBackupJob,
  SetupInput,
} from "./ports";
import { SessionError } from "./ports";
import { decode, type Reply, type Request, WireError } from "./http-transport";

export interface SessionTransport {
  send(request: Request): Promise<Reply>;
  adopt(reply: Reply): AuthenticatedSession;
  csrfToken(): string;
  authGeneration(): number;
  clearSession(generation: number): void;
  onExpired(listener: () => void): () => void;
}

function failure(wire: WireError): SessionError {
  if (wire.wire.code === "invalid_credentials")
    return new SessionError("invalid-credentials");
  if (wire.wire.code === "otp_required") return new SessionError("otp-required");
  if (wire.wire.code === "setup_unavailable")
    return new SessionError("setup-unavailable");
  if (wire.wire.code === "validation_failed")
    return new SessionError("invalid-input");
  return new SessionError("failed");
}

export function createSessionPort(transport: SessionTransport): SessionPort {
  const authenticate = async (path: string, body: unknown) => {
    try {
      return transport.adopt(
        await transport.send({ path, method: "POST", body, anonymous: true }),
      );
    } catch (caught) {
      throw caught instanceof WireError ? failure(caught) : caught;
    }
  };
  return {
    resume: async (signal?: AbortSignal): Promise<ResumedSession> => {
      try {
        return {
          kind: "authenticated",
          session: transport.adopt(
            await transport.send({
              path: "/api/admin/session",
              signal,
              anonymous: true,
            }),
          ),
        };
      } catch (caught) {
        if (!(caught instanceof WireError)) throw caught;
        if (caught.wire.status === 404 && caught.wire.code === "setup_required")
          return { kind: "setup-required" };
        if (caught.wire.status !== 401) return { kind: "unreachable" };
        try {
          const status = await transport.send({
            path: "/api/admin/status",
            signal,
            anonymous: true,
          });
          return (status.data as { setupRequired: boolean }).setupRequired
            ? { kind: "setup-required" }
            : { kind: "anonymous" };
        } catch (probe) {
          if (signal?.aborted) throw probe;
          return { kind: "setup-unknown" };
        }
      }
    },
    setup: (input: SetupInput) => authenticate("/api/admin/setup", input),
    login: (input: LoginInput) =>
      authenticate("/api/admin/auth/login", input),
    logout: async (): Promise<void> => {
      const generation = transport.authGeneration();
      try {
        await transport.send({
          path: "/api/admin/auth/logout",
          method: "POST",
          headers: {
            "X-CSRF-Token": transport.csrfToken(),
            "X-PgBase-CSRF": transport.csrfToken(),
          },
        });
      } catch (caught) {
        throw caught instanceof WireError ? failure(caught) : caught;
      }
      transport.clearSession(generation);
    },
    uploadSetupBackup: async (setupToken: string, archive: File, signal?: AbortSignal): Promise<SetupBackup> => {
      const response = await fetch("/api/admin/setup/backups/upload", {
        method: "POST", body: archive, credentials: "include", signal,
        headers: { "Content-Type": "application/gzip", "X-PgBase-Setup-Token": setupToken },
      });
      if (!response.ok)
        throw new SessionError(response.status === 401 ? "invalid-credentials" : "failed");
      return (await decode(response)) as SetupBackup;
    },
    restoreSetupBackup: async (setupToken, id, signal): Promise<SetupBackupJob> => {
      try {
        return (await transport.send({
          path: `/api/admin/setup/backups/${encodeURIComponent(id)}/restore`,
          method: "POST", headers: { "X-PgBase-Setup-Token": setupToken },
          anonymous: true, signal,
        })).data as SetupBackupJob;
      } catch (caught) {
        throw caught instanceof WireError && caught.wire.status === 401
          ? new SessionError("invalid-credentials") : caught;
      }
    },
    setupBackupJob: async (setupToken, id, signal): Promise<SetupBackupJob> => {
      const response = await fetch(`/api/admin/setup/backup-jobs/${encodeURIComponent(id)}`, {
        credentials: "include", signal, headers: { "X-PgBase-Setup-Token": setupToken },
      });
      if (!response.ok)
        throw new SessionError(response.status === 401 ? "invalid-credentials" : "failed");
      return (await decode(response)) as SetupBackupJob;
    },
    requestPasswordReset: async (email, signal) => {
      await transport.send({ path: "/api/admin/auth/request-password-reset", method: "POST", body: { email }, anonymous: true, signal });
    },
    confirmPasswordReset: async (token, newPassword, signal) => {
      await transport.send({ path: "/api/admin/auth/confirm-password-reset", method: "POST", body: { token, newPassword }, anonymous: true, signal });
    },
    onExpired: transport.onExpired,
  };
}
