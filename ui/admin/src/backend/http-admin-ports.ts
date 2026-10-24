import { createAuthFeaturePorts, createRealtimePort } from "./http-feature-ports";
import { providerInput, smtpInput, templateInput } from "./auth-input";
import type {
  AdminBackend, AdminSettings, AdministratorProfile, AuthAdministration, AuthCollection, AuthMailPreview, AuthMailTemplate, AuthPolicy, AuthSMTPSettings, BackupEntry, BackupJob, ImpersonatedUserSession, IssuedServiceToken, JobPage, ManagedUser, OAuthIdentity, OAuthProviderSettings, RecordRules, RequestLogEntry, RequestLogFilter, RequestLogPage, RequestLogStatistics, RulePreview, SchemaHealth, ServiceToken, ServiceTokenScope, SystemStatus, UserPage,
} from "./ports";
import { StorageMigrationNotFound, StorageMigrationRejected } from "./storage-port";
import type { BackupStorageSettings, BackupStorageUpdate } from "./backup-storage-port";
import type { FileStorageMigration, FileStorageMigrationInput, FileStorageSettings, FileStorageUpdate } from "./storage-port";
import { decode, WireError, type Reply, type Request } from "./http-transport";

export interface AdminPortsTransport {
  send(request: Request): Promise<Reply>;
  schemaCall(request: Request): Promise<Reply>;
  systemFailure(caught: unknown): unknown;
  csrfToken(): string;
  protectedFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>;
}

export function createAdminPorts(transport: AdminPortsTransport): Pick<AdminBackend, "system" | "settings" | "profile" | "operations" | "storage" | "backupStorage" | "users" | "rules" | "authCollections" | "serviceTokens" | "realtime"> {
  return {
    realtime: createRealtimePort(transport),
    system: {
    status: async (signal?: AbortSignal): Promise<SystemStatus> => {
      try {
        return (await transport.send({ path: "/api/admin/system", signal }))
          .data as SystemStatus;
      } catch (caught: unknown) {
        throw transport.systemFailure(caught);
      }
    },
    health: async (signal?: AbortSignal): Promise<SchemaHealth> => {
      try {
        return (await transport.send({ path: "/api/admin/system/health", signal }))
          .data as SchemaHealth;
      } catch (caught: unknown) {
        throw transport.systemFailure(caught);
      }
    },
    },

    settings: {
    get: async (signal?: AbortSignal): Promise<AdminSettings> =>
      (await transport.send({ path: "/api/admin/settings", signal }))
        .data as AdminSettings,
    save: async (
      value: AdminSettings,
      signal?: AbortSignal,
    ): Promise<AdminSettings> =>
      (
        await transport.send({
          path: "/api/admin/settings",
          method: "PATCH",
          body: value,
          csrf: true,
          signal,
        })
      ).data as AdminSettings,
    reload: async (signal?: AbortSignal): Promise<void> => {
      await transport.send({
        path: "/api/admin/settings/reload",
        method: "POST",
        csrf: true,
        signal,
      });
    },
    },
    profile: {
    get: async (signal?: AbortSignal): Promise<AdministratorProfile> =>
      (await transport.send({ path: "/api/admin/profile", signal }))
        .data as AdministratorProfile,
    update: async (
      value: { email: string },
      signal?: AbortSignal,
    ): Promise<AdministratorProfile> =>
      (
        await transport.send({
          path: "/api/admin/profile",
          method: "PATCH",
          body: value,
          csrf: true,
          signal,
        })
      ).data as AdministratorProfile,
    ipAllowlist: async (signal?: AbortSignal): Promise<string[]> =>
      (
        (await transport.send({ path: "/api/admin/profile/ip-allowlist", signal }))
          .data as { prefixes: string[] }
      ).prefixes,
    setIPAllowlist: async (
      prefixes: string[],
      signal?: AbortSignal,
    ): Promise<string[]> =>
      (
        (
          await transport.send({
            path: "/api/admin/profile/ip-allowlist",
            method: "PATCH",
            body: { prefixes },
            csrf: true,
            signal,
          })
        ).data as { prefixes: string[] }
      ).prefixes,
    },

    operations: {
    logs: async (
      before?: number,
      statusClass?: number,
      signal?: AbortSignal,
    ): Promise<RequestLogPage> => {
      const query = new URLSearchParams();
      if (before !== undefined) query.set("before", String(before));
      if (statusClass !== undefined)
        query.set("statusClass", String(statusClass));
      return (await transport.send({ path: `/api/admin/logs?${query}`, signal }))
        .data as RequestLogPage;
    },
    searchLogs: async (
      filter: RequestLogFilter,
      signal?: AbortSignal,
    ): Promise<RequestLogPage> => {
      const query = new URLSearchParams();
      for (const [key, value] of Object.entries(filter))
        if (value !== undefined && value !== "") query.set(key, String(value));
      return (await transport.send({ path: `/api/admin/logs?${query}`, signal }))
        .data as RequestLogPage;
    },
    logDetail: async (
      id: number,
      signal?: AbortSignal,
    ): Promise<RequestLogEntry> =>
      (
        await transport.send({
          path: `/api/admin/logs/${encodeURIComponent(id)}`,
          signal,
        })
      ).data as RequestLogEntry,
    logStatistics: async (
      signal?: AbortSignal,
    ): Promise<RequestLogStatistics> =>
      (await transport.send({ path: "/api/admin/logs/statistics", signal }))
        .data as RequestLogStatistics,
    truncateLogs: async (signal?: AbortSignal): Promise<void> => {
      await transport.send({
        path: "/api/admin/logs",
        method: "DELETE",
        csrf: true,
        signal,
      });
    },
    jobs: async (signal?: AbortSignal): Promise<JobPage> =>
      (await transport.send({ path: "/api/admin/jobs", signal })).data as JobPage,
    configureJob: async (
      id: string,
      schedule: string,
      enabled: boolean,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.send({
        path: `/api/admin/jobs/${encodeURIComponent(id)}`,
        method: "PUT",
        body: { schedule, enabled },
        csrf: true,
        signal,
      });
    },
    configureJobInTimezone: async (
      id: string,
      schedule: string,
      timezone: string,
      enabled: boolean,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.send({
        path: `/api/admin/jobs/${encodeURIComponent(id)}`,
        method: "PUT",
        body: { schedule, timezone, enabled },
        csrf: true,
        signal,
      });
    },
    runJob: async (id: string, signal?: AbortSignal): Promise<void> => {
      await transport.send({
        path: `/api/admin/jobs/${encodeURIComponent(id)}/run`,
        method: "POST",
        csrf: true,
        signal,
      });
    },
    backups: async (signal?: AbortSignal): Promise<BackupEntry[] | null> => {
      try {
        return (
          (await transport.send({ path: "/api/admin/backups", signal })).data as {
            items: BackupEntry[];
          }
        ).items;
      } catch (error) {
        if (error instanceof WireError && error.wire.status === 404)
          return null;
        throw transport.systemFailure(error);
      }
    },
    createBackup: async (signal?: AbortSignal): Promise<BackupJob> =>
      (
        await transport.send({
          path: "/api/admin/backups",
          method: "POST",
          csrf: true,
          signal,
        })
      ).data as BackupJob,
    uploadBackup: async (
      archive: File,
      signal?: AbortSignal,
    ): Promise<BackupEntry> => {
      const response = await transport.protectedFetch("/api/admin/backups/upload", {
        method: "POST",
        body: archive,
        headers: {
          "Content-Type": "application/gzip",
          "X-PgBase-CSRF": transport.csrfToken(),
        },
        credentials: "include",
        signal,
      });
      if (!response.ok) throw new Error(`backup_upload_${response.status}`);
      return (await decode(response)) as BackupEntry;
    },
    backupDownloadURL: (id: string): string =>
      `/api/admin/backups/${encodeURIComponent(id)}`,
    downloadBackup: async (id: string, signal?: AbortSignal): Promise<Blob> => {
      const response = await transport.protectedFetch(
        `/api/admin/backups/${encodeURIComponent(id)}`,
        { credentials: "include", signal },
      );
      if (!response.ok) throw new Error(`backup_download_${response.status}`);
      return response.blob();
    },
    deleteBackup: async (id: string, signal?: AbortSignal): Promise<void> => {
      await transport.send({
        path: `/api/admin/backups/${encodeURIComponent(id)}`,
        method: "DELETE",
        csrf: true,
        signal,
      });
    },
    restoreBackup: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<BackupJob> =>
      (
        await transport.send({
          path: `/api/admin/backups/${encodeURIComponent(id)}/restore`,
          method: "POST",
          csrf: true,
          signal,
        })
      ).data as BackupJob,
    backupJob: async (id: string, signal?: AbortSignal): Promise<BackupJob> =>
      (
        await transport.send({
          path: `/api/admin/backup-jobs/${encodeURIComponent(id)}`,
          signal,
        })
      ).data as BackupJob,
    },

    storage: {
    getFiles: async (signal?: AbortSignal): Promise<FileStorageSettings> =>
      (await transport.send({ path: "/api/admin/storage/files", signal }))
        .data as FileStorageSettings,
    saveFiles: async (
      input: FileStorageUpdate,
      signal?: AbortSignal,
    ): Promise<FileStorageSettings> =>
      (
        await transport.send({
          path: "/api/admin/storage/files",
          method: "PUT",
          body: input,
          csrf: true,
          signal,
        })
      ).data as FileStorageSettings,
    testFiles: async (signal?: AbortSignal): Promise<void> => {
      await transport.send({
        path: "/api/admin/storage/files/test",
        method: "POST",
        csrf: true,
        signal,
      });
    },
    previewMigration: async (
      input: FileStorageMigrationInput,
      signal?: AbortSignal,
    ): Promise<FileStorageMigration> =>
      (
        await transport.send({
          path: "/api/admin/storage/files/migrations/preview",
          method: "POST",
          body: input,
          csrf: true,
          signal,
        })
      ).data as FileStorageMigration,
    startMigration: async (
      input: FileStorageMigrationInput,
      operationID: string,
      signal?: AbortSignal,
    ): Promise<FileStorageMigration> => {
      try {
        return (
          await transport.send({
            path: "/api/admin/storage/files/migrations",
            method: "POST",
            body: input,
            headers: { "PgBase-Operation-ID": operationID },
            csrf: true,
            signal,
          })
        ).data as FileStorageMigration;
      } catch (error) {
        if (
          error instanceof WireError &&
          error.wire.status >= 400 &&
          error.wire.status < 500
        )
          throw new StorageMigrationRejected(error.wire.code);
        throw transport.systemFailure(error);
      }
    },
    migration: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<FileStorageMigration> => {
      try {
        return (
          await transport.send({
            path: `/api/admin/storage/files/migrations/${encodeURIComponent(id)}`,
            signal,
          })
        ).data as FileStorageMigration;
      } catch (error) {
        if (error instanceof WireError && error.wire.status === 404)
          throw new StorageMigrationNotFound();
        throw transport.systemFailure(error);
      }
    },
    latestMigration: async (
      signal?: AbortSignal,
    ): Promise<FileStorageMigration | null> => {
      try {
        return (
          await transport.send({
            path: "/api/admin/storage/files/migrations/latest",
            signal,
          })
        ).data as FileStorageMigration;
      } catch (error) {
        if (error instanceof WireError && error.wire.status === 404)
          return null;
        throw transport.systemFailure(error);
      }
    },
    resumeMigration: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.send({
        path: `/api/admin/storage/files/migrations/${encodeURIComponent(id)}/resume`,
        method: "POST",
        csrf: true,
        signal,
      });
    },
    cancelMigration: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.send({
        path: `/api/admin/storage/files/migrations/${encodeURIComponent(id)}/cancel`,
        method: "POST",
        csrf: true,
        signal,
      });
    },
    },

    backupStorage: {
    get: async (signal?: AbortSignal): Promise<BackupStorageSettings> =>
      (await transport.send({ path: "/api/admin/storage/backups", signal }))
        .data as BackupStorageSettings,
    save: async (
      input: BackupStorageUpdate,
      signal?: AbortSignal,
    ): Promise<BackupStorageSettings> =>
      (
        await transport.send({
          path: "/api/admin/storage/backups",
          method: "PUT",
          body: input,
          csrf: true,
          signal,
        })
      ).data as BackupStorageSettings,
    test: async (signal?: AbortSignal): Promise<void> => {
      await transport.send({
        path: "/api/admin/storage/backups/test",
        method: "POST",
        csrf: true,
        signal,
      });
    },
    },

    users: {
    list: async (after?: string, signal?: AbortSignal): Promise<UserPage> => {
      const query = new URLSearchParams({ limit: "50" });
      if (after) query.set("after", after);
      return (await transport.send({ path: `/api/admin/users?${query}`, signal }))
        .data as UserPage;
    },
    setActive: async (id: string, active: boolean): Promise<ManagedUser> =>
      (
        await transport.send({
          path: `/api/admin/users/${encodeURIComponent(id)}`,
          method: "PATCH",
          body: { active },
          csrf: true,
        })
      ).data as ManagedUser,
    revokeSessions: async (id: string): Promise<number> =>
      Number(
        (
          (
            await transport.send({
              path: `/api/admin/users/${encodeURIComponent(id)}/revoke-sessions`,
              method: "POST",
              csrf: true,
            })
          ).data as { revokedSessions: number }
        ).revokedSessions,
      ),
    setVerified: async (id: string, verified: boolean): Promise<ManagedUser> =>
      (
        await transport.send({
          path: `/api/admin/users/${encodeURIComponent(id)}/verification`,
          method: "PUT",
          body: { verified },
          csrf: true,
        })
      ).data as ManagedUser,
    sendVerification: async (id: string): Promise<void> => {
      await transport.send({
        path: `/api/admin/users/${encodeURIComponent(id)}/send-verification`,
        method: "POST",
        csrf: true,
      });
    },
    sendPasswordReset: async (id: string): Promise<void> => {
      await transport.send({
        path: `/api/admin/users/${encodeURIComponent(id)}/send-password-reset`,
        method: "POST",
        csrf: true,
      });
    },
    impersonate: async (id: string, durationSeconds = 300) =>
      (
        await transport.send({
          path: `/api/admin/users/${encodeURIComponent(id)}/impersonate`,
          method: "POST",
          body: { durationSeconds },
          csrf: true,
        })
      ).data as ImpersonatedUserSession,
    oauthIdentities: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<OAuthIdentity[]> =>
      (
        (
          await transport.send({
            path: `/api/admin/users/${encodeURIComponent(id)}/oauth-identities`,
            signal,
          })
        ).data as { items: OAuthIdentity[] }
      ).items,
    unlinkOAuthIdentity: async (
      id: string,
      provider: string,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.send({
        path: `/api/admin/users/${encodeURIComponent(id)}/oauth-identities/${encodeURIComponent(provider)}`,
        method: "DELETE",
        csrf: true,
        signal,
      });
    },
    },

    rules: {
    getRules: async (
      collection: string,
      signal?: AbortSignal,
    ): Promise<RecordRules> =>
      (
        await transport.schemaCall({
          path: `/api/admin/collections/${encodeURIComponent(collection)}/rules`,
          signal,
        })
      ).data as RecordRules,
    setRules: async (
      collection: string,
      rules: Record<string, unknown>,
      signal?: AbortSignal,
    ): Promise<RecordRules> =>
      (
        await transport.schemaCall({
          path: `/api/admin/collections/${encodeURIComponent(collection)}/rules`,
          method: "PUT",
          body: { rules },
          csrf: true,
          signal,
        })
      ).data as RecordRules,
    previewRule: async (
      collection: string,
      value: {
        rule: Record<string, unknown>;
        action: "list" | "view" | "update" | "delete";
        userId?: string;
        recordIds: string[];
      },
      signal?: AbortSignal,
    ): Promise<RulePreview> =>
      (
        await transport.schemaCall({
          path: `/api/admin/collections/${encodeURIComponent(collection)}/rules/preview`,
          method: "POST",
          body: value,
          csrf: true,
          signal,
        })
      ).data as RulePreview,
    },
    authCollections: {
    ...createAuthFeaturePorts(transport),
    listAuthCollections: async (
      signal?: AbortSignal,
    ): Promise<AuthCollection[]> =>
      (
        (
          await transport.schemaCall({
            path: "/api/admin/auth-collections",
            signal,
          })
        ).data as { items: AuthCollection[] }
      ).items,
    createAuthCollection: async (
      value: AuthCollection,
      signal?: AbortSignal,
    ): Promise<AuthCollection> =>
      (
        await transport.schemaCall({
          path: "/api/admin/auth-collections",
          method: "POST",
          body: value,
          csrf: true,
          signal,
        })
      ).data as AuthCollection,
    updateAuthCollection: async (
      id: string,
      value: Pick<AuthCollection, "registrationEnabled" | "profileSchema" | "fields" | "rules" | "recordId">,
      signal?: AbortSignal,
    ): Promise<AuthCollection> =>
      (
        await transport.schemaCall({
          path: `/api/admin/auth-collections/${encodeURIComponent(id)}`,
          method: "PATCH",
          body: value,
          csrf: true,
          signal,
        })
      ).data as AuthCollection,
    deleteAuthCollection: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.schemaCall({
        path: `/api/admin/auth-collections/${encodeURIComponent(id)}`,
        method: "DELETE",
        csrf: true,
        signal,
      });
    },
    getAdministration: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<AuthAdministration> =>
      (
        await transport.schemaCall({
          path: `/api/admin/auth-collections/${encodeURIComponent(id)}/administration`,
          signal,
        })
      ).data as AuthAdministration,
    setPolicy: async (
      id: string,
      value: AuthPolicy,
      signal?: AbortSignal,
    ): Promise<AuthPolicy> =>
      (
        await transport.schemaCall({
          path: `/api/admin/auth-collections/${encodeURIComponent(id)}/policy`,
          method: "PUT",
          body: value,
          csrf: true,
          signal,
        })
      ).data as AuthPolicy,
    putProvider: async (
      id: string,
      value: OAuthProviderSettings,
      signal?: AbortSignal,
    ): Promise<OAuthProviderSettings> =>
      (
        await transport.schemaCall({
          path: `/api/admin/auth-collections/${encodeURIComponent(id)}/oauth-providers/${encodeURIComponent(value.name)}`,
          method: "PUT",
          body: providerInput(value),
          csrf: true,
          signal,
        })
      ).data as OAuthProviderSettings,
    deleteProvider: async (
      id: string,
      name: string,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.schemaCall({
        path: `/api/admin/auth-collections/${encodeURIComponent(id)}/oauth-providers/${encodeURIComponent(name)}`,
        method: "DELETE",
        csrf: true,
        signal,
      });
    },
    putTemplate: async (
      id: string,
      value: AuthMailTemplate,
      signal?: AbortSignal,
    ): Promise<AuthMailTemplate> =>
      (
        await transport.schemaCall({
          path: `/api/admin/auth-collections/${encodeURIComponent(id)}/mail-templates/${value.purpose}`,
          method: "PUT",
          body: templateInput(value),
          csrf: true,
          signal,
        })
      ).data as AuthMailTemplate,
    previewTemplate: async (
      id: string,
      value: AuthMailTemplate & { actionUrl: string },
      signal?: AbortSignal,
    ): Promise<AuthMailPreview> =>
      (
        await transport.schemaCall({
          path: `/api/admin/auth-collections/${encodeURIComponent(id)}/mail-templates/${value.purpose}/preview`,
          method: "POST",
          body: { ...templateInput(value), actionUrl: value.actionUrl },
          csrf: true,
          signal,
        })
      ).data as AuthMailPreview,
    putSMTP: async (
      id: string,
      value: AuthSMTPSettings,
      signal?: AbortSignal,
    ): Promise<AuthSMTPSettings> =>
      (
        await transport.schemaCall({
          path: `/api/admin/auth-collections/${encodeURIComponent(id)}/smtp`,
          method: "PUT",
          body: smtpInput(value),
          csrf: true,
          signal,
        })
      ).data as AuthSMTPSettings,
    testSMTP: async (
      id: string,
      recipient: string,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.schemaCall({
        path: `/api/admin/auth-collections/${encodeURIComponent(id)}/smtp/test`,
        method: "POST",
        body: { recipient },
        csrf: true,
        signal,
      });
    },
    generateAppleClientSecret: async (
      value: {
        clientId: string;
        teamId: string;
        keyId: string;
        privateKey: string;
        duration: number;
      },
      signal?: AbortSignal,
    ): Promise<{ secret: string }> =>
      (
        await transport.schemaCall({
          path: "/api/admin/settings/apple/generate-client-secret",
          method: "POST",
          body: value,
          csrf: true,
          signal,
        })
      ).data as { secret: string },
    },
    serviceTokens: {
    listServiceTokens: async (signal?: AbortSignal): Promise<ServiceToken[]> =>
      (
        (await transport.schemaCall({ path: "/api/admin/service-tokens", signal }))
          .data as { items: ServiceToken[] }
      ).items,
    issueServiceToken: async (
      value: { name: string; ttlSeconds: number; scopes: ServiceTokenScope[] },
      signal?: AbortSignal,
    ): Promise<IssuedServiceToken> =>
      (
        await transport.schemaCall({
          path: "/api/admin/service-tokens",
          method: "POST",
          body: value,
          csrf: true,
          signal,
        })
      ).data as IssuedServiceToken,
    revokeServiceToken: async (
      id: string,
      signal?: AbortSignal,
    ): Promise<void> => {
      await transport.schemaCall({
        path: `/api/admin/service-tokens/${encodeURIComponent(id)}/revoke`,
        method: "POST",
        csrf: true,
        signal,
      });
    },
    },
  };
}
