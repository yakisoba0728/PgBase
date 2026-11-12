import { request } from "./http";

export interface ManagedJob {
  id: string;
  name: string;
  schedule: string;
  timezone: string;
  enabled: boolean;
  registered: boolean;
  running: boolean;
  nextRunAt: string;
  lastStartedAt?: string;
  lastFinishedAt?: string;
  attempts: number;
  failed: boolean;
}

export interface ManagedJobList {
  items: ManagedJob[];
  /** False when this process runs no job worker; update/run endpoints are then absent. */
  workerEnabled: boolean;
}

export interface ManagedJobUpdate {
  schedule: string;
  /** IANA zone; empty or omitted means "UTC". */
  timezone?: string;
  enabled: boolean;
}

export async function listJobs(signal?: AbortSignal): Promise<ManagedJobList> {
  return request<ManagedJobList>("GET", "/api/admin/jobs", { signal });
}

export async function updateJob(id: string, input: ManagedJobUpdate): Promise<void> {
  await request<void>("PUT", `/api/admin/jobs/${encodeURIComponent(id)}`, {
    body: { schedule: input.schedule, timezone: input.timezone, enabled: input.enabled },
  });
}

export async function runJob(id: string): Promise<void> {
  await request<void>("POST", `/api/admin/jobs/${encodeURIComponent(id)}/run`);
}
