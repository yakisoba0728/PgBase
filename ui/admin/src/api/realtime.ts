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

/** Revives a dead delivery; a no-op for events that are not dead. */
export async function retryRealtimeEvent(eventId: string): Promise<void> {
  await request<void>("POST", `/api/admin/realtime/${encodeURIComponent(eventId)}/retry`);
}
