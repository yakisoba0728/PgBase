import { queryOptions, useQuery } from "@tanstack/react-query";
import { getBackupJob, listBackups, listRunningBackupJobs } from "../api/backups";
import { ApiError, isNotFound } from "../api/http";
import { getBackupStorageSettings, getFileStorageSettings, getLatestStorageMigration, getStorageMigration, type BackupStorageSettings, type FileStorageSettings, type StorageMigrationState, type StorageMigrationStatus } from "../api/storage";
import { operations, startWindow, type TrackedOperation } from "../app/operations";

export const backupsQuery = queryOptions({
  queryKey: ["backups", "list"],
  queryFn: ({ signal }) => listBackups(signal),
});

/**
 * The backup console is opt-in (PGBASE_BACKUPS_DIRECTORY): a server without it never registered the list route, so it
 * answers 404. `listed` (the list has loaded) gates the console's other reads, which would 404 alongside it.
 */
export function useBackupsConsole(): { off: boolean; listed: boolean } {
  const list = useQuery(backupsQuery);
  return { off: list.data === undefined && isNotFound(list.error), listed: list.data !== undefined };
}

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

/** Operations running on the server, so another tab or browser can re-attach to one. */
export const runningBackupJobsQuery = queryOptions({
  queryKey: ["backups", "running"],
  queryFn: ({ signal }) => listRunningBackupJobs(signal),
});

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

/**
 * A start this browser sent (tracked since `startedAt`) whose migration the server has not shown yet, as of
 * `answeredAt`: whatever the query fell back to is an older migration. Past the start window it never arrived.
 */
export const awaitingStart = (id: string | null, data: StorageMigrationStatus | null | undefined, startedAt: number | undefined, answeredAt: number) =>
  id !== null && startedAt !== undefined && answeredAt - startedAt < startWindow && data?.id !== id;

export const migrationStartedAt = (operations: TrackedOperation[], id: string | null) => operations.find((operation) => operation.kind === "migration" && operation.id === id)?.startedAt;

/** The migration named in the URL (its start may never have arrived), else the latest one; null when none has ever run. */
export const migrationQuery = (id: string | null) =>
  queryOptions({
    queryKey: ["storage", "migration", id ?? "latest"],
    queryFn: async ({ signal }) => (id && (await orNull(getStorageMigration(id, signal)))) || orNull(getLatestStorageMigration(signal)),
    // Also polls while a tracked start has not appeared, until its start window ends.
    refetchInterval: (query) =>
      migrationRunning(query.state.data?.status) || awaitingStart(id, query.state.data, migrationStartedAt(operations(), id), query.state.dataUpdatedAt) ? 1000 : false,
  });
