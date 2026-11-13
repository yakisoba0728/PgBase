import { request } from "./http";
import type { Int64 } from "./logs";

export interface RealtimeStatus {
  pending: Int64;
  dead: Int64;
  delivered: Int64;
  oldestPending?: string;
  cursorFloor: Int64;
}

export async function getRealtimeStatus(signal?: AbortSignal): Promise<RealtimeStatus> {
  return request<RealtimeStatus>("GET", "/api/admin/realtime/status", { signal });
}

export interface DeadRealtimeEvent {
  eventId: string;
  collectionName: string;
  recordId: string;
  operation: "create" | "update" | "delete";
  recordVersion: Int64;
  attempts: number;
  createdAt: string;
  deadAt: string;
}

export interface DeadRealtimePage {
  items: DeadRealtimeEvent[];
  /** Opaque; pass as `before` for the next (older) page. */
  nextBefore?: string;
}

/** Dead deliveries, newest first, 50 per page. */
export async function listDeadRealtimeEvents(before?: string, signal?: AbortSignal): Promise<DeadRealtimePage> {
  return request<DeadRealtimePage>("GET", "/api/admin/realtime/dead", { query: { before }, signal });
}

/** Revives a dead delivery; a no-op for events that are not dead. */
export async function retryRealtimeEvent(eventId: string): Promise<void> {
  await request<void>("POST", `/api/admin/realtime/${encodeURIComponent(eventId)}/retry`);
}
