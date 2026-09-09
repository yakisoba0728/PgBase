export interface BackupStorageSettings {
  driver: "local" | "s3";
  bucket: string;
  prefix: string;
  region: string;
  endpoint: string;
  accessKey: string;
  usePathStyle: boolean;
  retention: number;
  secretConfigured: boolean;
  revision: number;
}
export interface BackupStorageUpdate {
  driver: "local" | "s3";
  bucket: string;
  prefix: string;
  region: string;
  endpoint: string;
  accessKey: string;
  secretKey?: string;
  clearSecret?: boolean;
  usePathStyle: boolean;
  retention: number;
  revision: number;
}
export interface BackupStoragePort {
  get(signal?: AbortSignal): Promise<BackupStorageSettings>;
  save(input: BackupStorageUpdate, signal?: AbortSignal): Promise<BackupStorageSettings>;
  test(signal?: AbortSignal): Promise<void>;
}
