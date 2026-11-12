import type { LosslessNumber } from "lossless-json";
import { request } from "./http";

/** A 64-bit server integer: a number when exact, otherwise the server's text. Render with String(). */
export type Int64 = number | LosslessNumber;

export interface RequestLogEntry {
  id: Int64;
  at: string;
  method: string;
  route: string;
  status: number;
  durationMs: number;
  clientIP?: string;
  authKind?: string;
  authID?: string;
}

export interface RequestLogPage {
  items: RequestLogEntry[];
  /** Pass as `before` to load the next (older) page; absent on the last page. */
  nextBefore?: Int64;
  oldestId: Int64;
}

export interface RequestLogFilter {
  /** Return entries with id lower than this (pagination cursor, > 0). */
  before?: string;
  /** 1–5, matching 1xx..5xx. */
  statusClass?: 1 | 2 | 3 | 4 | 5;
  method?: string;
  route?: string;
  /** Case-insensitive substring of the route, client IP or auth kind/id (at most 256 characters). */
  q?: string;
  /** RFC 3339 timestamp. */
  from?: string;
  /** RFC 3339 timestamp. */
  to?: string;
}

export interface RequestLogStatistics {
  total: Int64;
  dropped: Int64;
  oldestAt?: string;
  newestAt?: string;
  averageDurationMs: number;
  /** Keyed by class label, e.g. "2xx", "4xx". */
  statusClasses: Record<string, Int64>;
}

export async function listRequestLogs(filter: RequestLogFilter = {}, signal?: AbortSignal): Promise<RequestLogPage> {
  return request<RequestLogPage>("GET", "/api/admin/logs", { query: { ...filter }, signal });
}

/** Deletes every request log entry and restarts ids at 1. */
export async function clearRequestLogs(): Promise<void> {
  await request<void>("DELETE", "/api/admin/logs");
}

/** Deletes entries older than `to` (RFC 3339); ids are kept. */
export async function deleteRequestLogsBefore(to: string): Promise<{ deleted: Int64 }> {
  return request<{ deleted: Int64 }>("DELETE", "/api/admin/logs", { query: { to } });
}

/** Statistics for the optional [from, to) window; `dropped` is process-wide. */
export async function getRequestLogStatistics(range: Pick<RequestLogFilter, "from" | "to"> = {}, signal?: AbortSignal): Promise<RequestLogStatistics> {
  return request<RequestLogStatistics>("GET", "/api/admin/logs/statistics", { query: { ...range }, signal });
}

export async function getRequestLog(id: string, signal?: AbortSignal): Promise<RequestLogEntry> {
  return request<RequestLogEntry>("GET", `/api/admin/logs/${encodeURIComponent(id)}`, { signal });
}
