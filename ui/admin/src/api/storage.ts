import { request } from "./http";

export type StorageDriver = "local" | "s3";

/** Secret handling on writes: `secretKey` is write-only and never returned.
 * - `secretKey: ""` and `clearSecret: false` keeps the stored secret.
 * - non-empty `secretKey` replaces it.
 * - `clearSecret: true` removes it (takes precedence over `secretKey`).
 * Reads only report whether one is stored via `secretConfigured`.
 * For s3, `accessKey` and the resulting stored secret must be both set or both empty (else 400).
 * Bodies are decoded strictly: unknown fields are rejected. */
export interface StorageSecretInput {
  accessKey: string;
  /** Write-only. Empty string keeps the stored secret unless `clearSecret` is true. */
  secretKey: string;
  clearSecret: boolean;
}

export interface FileStorageSettings {
  driver: StorageDriver;
  bucket: string;
  region: string;
  endpoint: string;
  accessKey: string;
  usePathStyle: boolean;
  secretConfigured: boolean;
  revision: number;
}

export interface FileStorageDestination extends StorageSecretInput {
  driver: StorageDriver;
  bucket: string;
  region: string;
  endpoint: string;
  usePathStyle: boolean;
}

export interface FileStorageUpdate extends FileStorageDestination {
  /** Revision last read; a stale value yields 409 storage_conflict. */
  revision: number;
}

export type StorageMigrationState =
  | "preview"
  | "copying"
  | "canceling"
  | "ready"
  | "activating"
  | "committed"
  | "canceled"
  | "failed";

export interface StorageMigrationStatus {
  /** Zero UUID for previews. */
  id: string;
  status: StorageMigrationState;
  copiedObjects: number;
  totalObjects: number;
  failure?: string;
  createdAt: string;
  updatedAt: string;
}

export interface BackupStorageSettings {
  driver: StorageDriver;
  bucket: string;
  prefix: string;
  region: string;
  endpoint: string;
  accessKey: string;
  usePathStyle: boolean;
  /** Number of archives to keep; 0 keeps every archive. */
  retention: number;
  secretConfigured: boolean;
  revision: number;
}

export interface BackupStorageUpdate extends StorageSecretInput {
  driver: StorageDriver;
  bucket: string;
  prefix: string;
  region: string;
  endpoint: string;
  usePathStyle: boolean;
  retention: number;
  /** Revision last read; a stale value yields 409 storage_conflict. */
  revision: number;
}

export async function getFileStorageSettings(signal?: AbortSignal): Promise<FileStorageSettings> {
  return request<FileStorageSettings>("GET", "/api/admin/storage/files", { signal });
}

export async function updateFileStorageSettings(input: FileStorageUpdate): Promise<FileStorageSettings> {
  return request<FileStorageSettings>("PUT", "/api/admin/storage/files", { body: input });
}

export async function testFileStorage(): Promise<void> {
  await request<void>("POST", "/api/admin/storage/files/test");
}

// Strict server decoding: send only destination fields even if a FileStorageUpdate is passed.
function destinationBody(d: FileStorageDestination): FileStorageDestination {
  const { driver, bucket, region, endpoint, accessKey, secretKey, usePathStyle, clearSecret } = d;
  return { driver, bucket, region, endpoint, accessKey, secretKey, usePathStyle, clearSecret };
}

export async function previewStorageMigration(destination: FileStorageDestination): Promise<StorageMigrationStatus> {
  return request<StorageMigrationStatus>("POST", "/api/admin/storage/files/migrations/preview", { body: destinationBody(destination) });
}

/** `operationId` is a client-generated UUID that makes the start idempotent. */
export async function startStorageMigration(operationId: string, destination: FileStorageDestination): Promise<StorageMigrationStatus> {
  return request<StorageMigrationStatus>("POST", "/api/admin/storage/files/migrations", {
    body: destinationBody(destination),
    headers: { "PgBase-Operation-ID": operationId },
  });
}

/** Rejects with 404 not_found when no migration has ever run. */
export async function getLatestStorageMigration(signal?: AbortSignal): Promise<StorageMigrationStatus> {
  return request<StorageMigrationStatus>("GET", "/api/admin/storage/files/migrations/latest", { signal });
}

export async function getStorageMigration(id: string, signal?: AbortSignal): Promise<StorageMigrationStatus> {
  return request<StorageMigrationStatus>("GET", `/api/admin/storage/files/migrations/${encodeURIComponent(id)}`, { signal });
}

export async function cancelStorageMigration(id: string): Promise<StorageMigrationStatus> {
  return request<StorageMigrationStatus>("POST", `/api/admin/storage/files/migrations/${encodeURIComponent(id)}/cancel`);
}

export async function resumeStorageMigration(id: string): Promise<StorageMigrationStatus> {
  return request<StorageMigrationStatus>("POST", `/api/admin/storage/files/migrations/${encodeURIComponent(id)}/resume`);
}

export async function getBackupStorageSettings(signal?: AbortSignal): Promise<BackupStorageSettings> {
  return request<BackupStorageSettings>("GET", "/api/admin/storage/backups", { signal });
}

export async function updateBackupStorageSettings(input: BackupStorageUpdate): Promise<BackupStorageSettings> {
  return request<BackupStorageSettings>("PUT", "/api/admin/storage/backups", { body: input });
}

export async function testBackupStorage(): Promise<void> {
  await request<void>("POST", "/api/admin/storage/backups/test");
}
