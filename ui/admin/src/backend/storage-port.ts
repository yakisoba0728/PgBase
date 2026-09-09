export interface FileStorageSettings {
  driver: "local" | "s3";
  bucket: string;
  region: string;
  endpoint: string;
  accessKey: string;
  usePathStyle: boolean;
  secretConfigured: boolean;
  revision: number;
}
export interface FileStorageUpdate {
  driver: "local" | "s3";
  bucket: string;
  region: string;
  endpoint: string;
  accessKey: string;
  secretKey?: string;
  clearSecret?: boolean;
  usePathStyle: boolean;
  revision: number;
}
export type FileStorageMigrationInput = Omit<FileStorageUpdate, "revision">;
export interface FileStorageMigration {
  id: string;
  status: "preview" | "copying" | "canceling" | "canceled" | "failed" | "ready" | "activating" | "committed";
  copiedObjects: number;
  totalObjects: number;
  failure?: string;
  createdAt?: string;
  updatedAt?: string;
}
export interface StoragePort {
  getFiles(signal?: AbortSignal): Promise<FileStorageSettings>;
  saveFiles(
    input: FileStorageUpdate,
    signal?: AbortSignal,
  ): Promise<FileStorageSettings>;
  testFiles(signal?: AbortSignal): Promise<void>;
  previewMigration(input: FileStorageMigrationInput, signal?: AbortSignal): Promise<FileStorageMigration>;
  startMigration(input: FileStorageMigrationInput, operationID: string, signal?: AbortSignal): Promise<FileStorageMigration>;
  migration(id: string, signal?: AbortSignal): Promise<FileStorageMigration>;
  latestMigration(signal?: AbortSignal): Promise<FileStorageMigration | null>;
  resumeMigration(id: string, signal?: AbortSignal): Promise<void>;
  cancelMigration(id: string, signal?: AbortSignal): Promise<void>;
}

export class StorageMigrationRejected extends Error {}

export class StorageMigrationNotFound extends Error {}
