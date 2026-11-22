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

function localIPv4(host: string): boolean {
  const parts = host.split(".");
  if (parts.length !== 4 || !parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)) return false;
  const [a, b] = parts.map(Number);
  return a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254);
}

function localIPv6(host: string): boolean {
  if (host === "::1") return true;
  // URL hostnames arrive compressed and lowercase, so a mapped IPv4 address is "::ffff:hhhh:hhhh".
  const mapped = /^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/.exec(host);
  if (mapped) {
    const high = parseInt(mapped[1], 16);
    const low = parseInt(mapped[2], 16);
    return localIPv4(`${high >> 8}.${high & 255}.${low >> 8}.${low & 255}`);
  }
  const first = host.startsWith("::") ? 0 : parseInt(host.split(":")[0], 16);
  return (first & 0xfe00) === 0xfc00 || (first & 0xffc0) === 0xfe80;
}

/**
 * Mirrors the server's plaintext-endpoint warning: plain http to anything but localhost or a
 * loopback, private or link-local IP literal sends backup credentials and archives unencrypted.
 * Plain http is still accepted; this only decides whether to warn.
 */
export function plaintextEndpoint(endpoint: string): boolean {
  let url: URL;
  try {
    url = new URL(endpoint.trim());
  } catch {
    return false;
  }
  if (url.protocol !== "http:") return false;
  const host = url.hostname.toLowerCase().replace(/\.$/, "");
  if (!host || host === "localhost" || host.endsWith(".localhost")) return false;
  if (host.startsWith("[") && host.endsWith("]")) return !localIPv6(host.slice(1, -1));
  return !localIPv4(host);
}

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
    if (!url || !bare || (url.protocol !== "https:" && !(url.protocol === "http:" && (withRetention || loopback.has(url.hostname))))) errors.endpoint = "storage.invalid.endpoint";
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
