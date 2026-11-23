import { request } from "./http";

export interface ManagedJob {
  id: string;
  name: string;
  schedule: string;
  timezone: string;
  enabled: boolean;
  registered: boolean;
  settingsManaged?: boolean;
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

/** `abandoned` is a run still recorded as running whose lease expired or was replaced. */
export type JobRunStatus = "running" | "succeeded" | "failed" | "superseded" | "abandoned";

export interface JobRun {
  id: number;
  status: JobRunStatus;
  /** 1 for an occurrence's first try, then one more per retry. */
  attempt: number;
  scheduledAt?: string;
  startedAt: string;
  finishedAt?: string;
  durationMs?: number;
  /** The handler's error text, at most 1,024 characters. */
  error?: string;
}

export interface JobRunPage {
  items: JobRun[];
  nextCursor?: string;
  retention: { maximumRuns: number; maximumAgeSeconds: number };
}

/** One job's runs, newest first; `cursor` is the previous page's `nextCursor`. */
export async function listJobRuns(id: string, cursor?: string, signal?: AbortSignal): Promise<JobRunPage> {
  return request<JobRunPage>("GET", `/api/admin/jobs/${encodeURIComponent(id)}/runs`, { query: { cursor }, signal });
}
