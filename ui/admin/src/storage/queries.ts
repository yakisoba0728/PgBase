import { queryOptions } from "@tanstack/react-query";
import { getBackupJob, listBackups } from "../api/backups";
import { ApiError } from "../api/http";
import { getBackupStorageSettings, getFileStorageSettings, getLatestStorageMigration, getStorageMigration, type BackupStorageSettings, type FileStorageSettings, type StorageMigrationState } from "../api/storage";

export const backupsQuery = queryOptions({
  queryKey: ["backups", "list"],
  queryFn: ({ signal }) => listBackups(signal),
});

export const backupStorageQuery = queryOptions({
  queryKey: ["storage", "backups"],
  queryFn: ({ signal }) => getBackupStorageSettings(signal),
});

export const fileStorageQuery = queryOptions({
  queryKey: ["storage", "files"],
  queryFn: ({ signal }) => getFileStorageSettings(signal),
});

/** Either settings screen; shares the cache entries above. */
export const storageSettingsQuery = (kind: "files" | "backups") =>
  queryOptions<FileStorageSettings | BackupStorageSettings>({
    queryKey: ["storage", kind],
    queryFn: ({ signal }) => (kind === "files" ? getFileStorageSettings(signal) : getBackupStorageSettings(signal)),
  });

const isNotFound = (error: unknown) => error instanceof ApiError && error.status === 404;

/** A backup job, polled until it finishes. Restore swaps the server underneath, so 503s and dropped connections keep polling. */
export const backupJobQuery = (id: string) =>
  queryOptions({
    queryKey: ["backups", "job", id],
    queryFn: ({ signal }) => getBackupJob(id, signal),
    retry: (count, error) => !isNotFound(error) && !(error instanceof ApiError && error.status === 401) && count < 30,
    retryDelay: 1000,
    refetchInterval: (query) => (query.state.data?.status === "running" || (!query.state.data && !isNotFound(query.state.error)) ? 1000 : false),
  });

const activeMigration = new Set<StorageMigrationState>(["copying", "canceling", "ready", "activating"]);
export const migrationRunning = (state: StorageMigrationState | undefined) => state !== undefined && activeMigration.has(state);

const orNull = <T,>(promise: Promise<T>) => promise.catch((error: unknown) => (isNotFound(error) ? null : Promise.reject(error)));

/** The migration named in the URL (its start may never have arrived), else the latest one; null when none has ever run. */
export const migrationQuery = (id: string | null) =>
  queryOptions({
    queryKey: ["storage", "migration", id ?? "latest"],
    queryFn: async ({ signal }) => (id && (await orNull(getStorageMigration(id, signal)))) || orNull(getLatestStorageMigration(signal)),
    refetchInterval: (query) => (migrationRunning(query.state.data?.status) ? 1000 : false),
  });
