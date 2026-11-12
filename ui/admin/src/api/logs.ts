import { request } from "./http";

export interface RequestLogEntry {
  id: number;
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
  nextBefore?: number;
  oldestId: number;
}

export interface RequestLogFilter {
  /** Return entries with id lower than this (pagination cursor, > 0). */
  before?: number;
  /** 1–5, matching 1xx..5xx. */
  statusClass?: 1 | 2 | 3 | 4 | 5;
  method?: string;
  route?: string;
  /** RFC 3339 timestamp. */
  from?: string;
  /** RFC 3339 timestamp. */
  to?: string;
}

export interface RequestLogStatistics {
  total: number;
  dropped: number;
  oldestAt?: string;
  newestAt?: string;
  averageDurationMs: number;
  /** Keyed by class label, e.g. "2xx", "4xx". */
  statusClasses: Record<string, number>;
}

export async function listRequestLogs(filter: RequestLogFilter = {}, signal?: AbortSignal): Promise<RequestLogPage> {
  return request<RequestLogPage>("GET", "/api/admin/logs", { query: { ...filter }, signal });
}

/** Deletes every request log entry (no filters). */
export async function clearRequestLogs(): Promise<void> {
  await request<void>("DELETE", "/api/admin/logs");
}

export async function getRequestLogStatistics(signal?: AbortSignal): Promise<RequestLogStatistics> {
  return request<RequestLogStatistics>("GET", "/api/admin/logs/statistics", { signal });
}

export async function getRequestLog(id: number, signal?: AbortSignal): Promise<RequestLogEntry> {
  return request<RequestLogEntry>("GET", `/api/admin/logs/${id}`, { signal });
}
