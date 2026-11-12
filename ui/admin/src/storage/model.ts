import { ApiError } from "../api/http";
import type { BackupStorageSettings, BackupStorageUpdate, FileStorageDestination, FileStorageSettings, FileStorageUpdate, StorageDriver } from "../api/storage";
import type { MessageKey } from "../i18n";

const knownPhases = new Set(["create", "restore", "waiting_for_snapshot", "dumping_database", "packing_archive", "validating_archive", "restoring_database_and_files", "verifying_application", "draining_requests", "activating_target", "completed", "failed", "interrupted"]);

/** A backup job phase as display text: known phases map to a message, object copying also yields a ratio. */
export function describePhase(phase: string): { key?: MessageKey; vars?: Record<string, number>; ratio?: number; raw: string } {
  const copying = /^copying_objects_(\d+)_of_(\d+)$/.exec(phase);
  if (copying) {
    const done = Number(copying[1]);
    const total = Number(copying[2]);
    return { key: "storage.phase.copying_objects", vars: { done, total }, ratio: total > 0 ? done / total : undefined, raw: phase };
  }
  return knownPhases.has(phase) ? { key: `storage.phase.${phase}` as MessageKey, raw: phase } : { raw: phase };
}

/** Editable storage settings; prefix and retention only apply to backup storage. */
export interface StorageDraft {
  driver: StorageDriver;
  bucket: string;
  prefix: string;
  region: string;
  endpoint: string;
  accessKey: string;
  secretKey: string;
  clearSecret: boolean;
  usePathStyle: boolean;
  retention: string;
}

type Saved = FileStorageSettings | BackupStorageSettings;

export function draftFrom(settings: Saved): StorageDraft {
  return {
    driver: settings.driver,
    bucket: settings.bucket,
    prefix: "prefix" in settings ? settings.prefix : "",
    region: settings.region,
    endpoint: settings.endpoint,
    accessKey: settings.accessKey,
    secretKey: "",
    clearSecret: false,
    usePathStyle: settings.usePathStyle,
    retention: "retention" in settings ? String(settings.retention) : "0",
  };
}

export const isDirty = (draft: StorageDraft, settings: Saved) => JSON.stringify(draft) !== JSON.stringify(draftFrom(settings));

export type DraftErrors = Partial<Record<keyof StorageDraft, MessageKey>>;

const loopback = new Set(["127.0.0.1", "localhost", "[::1]"]);

/** Mirrors the server's checks so a rejected save can point at a field (its 400 carries no details). */
export function validateDraft(draft: StorageDraft, secretConfigured: boolean, withRetention: boolean): DraftErrors {
  const errors: DraftErrors = {};
  if (withRetention && !/^\d+$/.test(draft.retention.trim())) errors.retention = "storage.invalid.retention";
  if (draft.driver === "local") return errors;
  if (!draft.bucket.trim()) errors.bucket = "storage.invalid.required";
  else if (draft.bucket.length > 255) errors.bucket = "storage.invalid.tooLong";
  if (!draft.region.trim()) errors.region = "storage.invalid.required";
  else if (draft.region.length > 128) errors.region = "storage.invalid.tooLong";
  const endpoint = draft.endpoint.trim();
  if (endpoint) {
    let url: URL | undefined;
    try {
      url = new URL(endpoint);
    } catch {
      url = undefined;
    }
    const bare = url && !url.username && !url.password && !url.search && !url.hash && (url.pathname === "/" || url.pathname === "");
    if (!url || !bare || (url.protocol !== "https:" && !(url.protocol === "http:" && loopback.has(url.hostname)))) errors.endpoint = "storage.invalid.endpoint";
  }
  const hasSecret = !draft.clearSecret && (draft.secretKey !== "" || secretConfigured);
  if (draft.accessKey.trim() && !hasSecret) errors.secretKey = "storage.invalid.secretMissing";
  if (!draft.accessKey.trim() && hasSecret) errors.accessKey = "storage.invalid.accessMissing";
  return errors;
}

/** The destination fields the server accepts; a local driver blanks the S3 fields and drops any stored secret. */
export function destinationOf(draft: StorageDraft): FileStorageDestination {
  if (draft.driver === "local") {
    return { driver: "local", bucket: "", region: "", endpoint: "", accessKey: "", secretKey: "", clearSecret: true, usePathStyle: false };
  }
  return {
    driver: "s3",
    bucket: draft.bucket.trim(),
    region: draft.region.trim(),
    endpoint: draft.endpoint.trim(),
    accessKey: draft.accessKey.trim(),
    secretKey: draft.clearSecret ? "" : draft.secretKey,
    clearSecret: draft.clearSecret,
    usePathStyle: draft.usePathStyle,
  };
}

export const fileUpdate = (draft: StorageDraft, revision: number): FileStorageUpdate => ({ ...destinationOf(draft), revision });

export function backupUpdate(draft: StorageDraft, revision: number): BackupStorageUpdate {
  return { ...destinationOf(draft), prefix: draft.driver === "s3" ? draft.prefix.trim() : "", retention: Number(draft.retention.trim()), revision };
}

/** Message for a failed storage call; the meaning of a conflict differs between file and backup storage. */
export function storageErrorKey(error: unknown, kind: "files" | "backups" | "migration"): MessageKey {
  if (!(error instanceof ApiError)) return "storage.error.generic";
  if (error.code === "invalid_request") return "storage.error.invalid";
  if (error.code === "storage_conflict") return `storage.error.conflict.${kind}`;
  if (error.code === "not_found") return "storage.error.notFound";
  if (error.status === 503) return "storage.error.unavailable";
  if (error.code === "network_error") return "storage.error.network";
  return "storage.error.generic";
}
