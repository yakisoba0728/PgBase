import { request, requestBlob } from "./http";

export interface BackupEntry {
  id: string;
  createdAt: string;
  size: number;
  postgresqlMajor: number;
  sha256?: string;
  storageFormat?: string;
}

export type BackupJobOperation = "create" | "restore";
export type BackupJobStatus = "running" | "completed" | "failed";

export interface BackupJob {
  id: string;
  operation: BackupJobOperation;
  backupId: string;
  status: BackupJobStatus;
  /** Free-form progress phase; "completed", "failed" or "interrupted" when finished. */
  phase: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

export async function listBackups(signal?: AbortSignal): Promise<BackupEntry[]> {
  const response = await request<{ items: BackupEntry[] }>("GET", "/api/admin/backups", { signal });
  return response.items;
}

/** Starts a backup; poll the returned job with getBackupJob. */
export async function createBackup(): Promise<BackupJob> {
  return request<BackupJob>("POST", "/api/admin/backups");
}

/** Uploads a gzip backup archive (.tar.gz) as the raw request body. */
export async function uploadBackup(archive: Blob, signal?: AbortSignal): Promise<BackupEntry> {
  return request<BackupEntry>("POST", "/api/admin/backups/upload", {
    raw: archive,
    headers: { "Content-Type": "application/gzip" },
    signal,
  });
}

/** Downloads the archive (served as pgbase-{id}.tar.gz). */
export async function downloadBackup(id: string, signal?: AbortSignal): Promise<Blob> {
  return requestBlob(`/api/admin/backups/${encodeURIComponent(id)}`, { signal });
}

export async function deleteBackup(id: string): Promise<void> {
  await request<void>("DELETE", `/api/admin/backups/${encodeURIComponent(id)}`);
}

/** Starts a restore; poll the returned job with getBackupJob. */
export async function restoreBackup(id: string): Promise<BackupJob> {
  return request<BackupJob>("POST", `/api/admin/backups/${encodeURIComponent(id)}/restore`);
}

export async function getBackupJob(id: string, signal?: AbortSignal): Promise<BackupJob> {
  return request<BackupJob>("GET", `/api/admin/backup-jobs/${encodeURIComponent(id)}`, { signal });
}
