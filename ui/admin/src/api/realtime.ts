import { request } from "./http";

export interface RealtimeStatus {
  pending: number;
  dead: number;
  delivered: number;
  oldestPending?: string;
  cursorFloor: number;
}

export async function getRealtimeStatus(signal?: AbortSignal): Promise<RealtimeStatus> {
  return request<RealtimeStatus>("GET", "/api/admin/realtime/status", { signal });
}

/** Revives a dead delivery; a no-op for events that are not dead. */
export async function retryRealtimeEvent(eventId: string): Promise<void> {
  await request<void>("POST", `/api/admin/realtime/${encodeURIComponent(eventId)}/retry`);
}
