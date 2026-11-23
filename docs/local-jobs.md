# Local scheduled jobs

Jobs are registered by the Go program that starts PgBase. Set
`PGBASE_JOBS_ENABLED=true` and add `ScheduledJob` entries before calling
`pgbase.New`. Schedules may use `@every <duration>` from one second through 24
hours or five-field cron with an explicit IANA timezone such as `UTC` or
`America/New_York`. Enter the timezone separately from the cron expression.
Existing schedules using `Local` or an inline timezone keep their prior
interpretation until edited. To update one, enter its effective timezone in
the timezone field and remove the inline prefix. The runner uses PostgreSQL's
clock for durable claims.

Run the local example after setting the normal database and token environment:

```sh
PGBASE_JOBS_ENABLED=true go run ./examples/jobs
```

The runner is at-least-once. A process can crash after an effect completes, or
a handler that ignores its context can outlive its lease and overlap a retry.
Handlers must honor their context. `JobRun.OccurrenceKey()` identifies one
logical scheduled occurrence: it stays stable across retries and changes for
the next schedule. Use it as the idempotency key for one effect per occurrence.
`JobRun.RunKey()` identifies a claim attempt and changes on retry. For effects
committed in PostgreSQL, record the highest `FenceGeneration` for the job and
reject older attempts in the same transaction.

## What `pgbase serve` registers

The standard binary has no compiled application jobs. Its job worker exists
when `PGBASE_BACKUPS_DIRECTORY` or `PGBASE_JOBS_ENABLED=true` is set:

- With a backups directory it always registers `pgbase.managed-backup`. Its
  schedule, timezone and enabled flag follow the `backups` runtime settings,
  so the job is listed but disabled until scheduled backups are turned on.
  Edit it under **Settings → Scheduled backups**. Use **Backups** to create
  an archive manually or review its outcome. The scheduler waits for the
  archive to be published. It renews the backup's lease while running, and
  failed or interrupted attempts retry the same occurrence. A retry reuses
  a published archive if the worker stopped before recording success.
  The row is created disabled. On an installation upgraded from a release
  that never registered it, the first registration clears stored
  `backups.enabled`/`backups.keep` values that had no effect (see
  [S3 backups](backup-s3.md)). Each run reloads the stored settings first, and
  the durable claim (`FOR UPDATE SKIP LOCKED` plus a lease) gives one occurrence
  to one instance when several share the database. Use a shared archive
  repository, such as the same S3 bucket and prefix, when backup workers run on
  more than one host.
- With `PGBASE_JOBS_ENABLED=true`, JavaScript jobs from `PGBASE_JS_DIR` are
  registered as well.

Without either, `GET /api/admin/jobs` returns `{"items":[],"workerEnabled":false}`
and the update and run routes are not mounted. That empty list is intended.

Ordinary handlers have a 30-second deadline and a one-minute lease. The
fixed relationship keeps cooperative handlers from being reclaimed while they
are still running. The managed backup runs separately, with a 30-minute
deadline and a one-minute lease renewed every 15 seconds. Neither lease
makes non-cooperative handlers exactly-once.

## Editing and requesting work

Configuration, disable/unregister, manual run requests, and the embedded Go
`Schedule` method refuse to replace an active lease. Wait for the current run
to complete or for its lease to expire. After expiry an accepted change clears
the old token and advances its generation atomically; a delayed old handler
cannot overwrite the operator's new state. Completion itself also requires an
unexpired lease, even when no other worker or operator has intervened.

A manual run request queues the normal worker. If an occurrence is already
waiting for retry, the request and a save with the same schedule and timezone
preserve its occurrence key and attempt history. A changed schedule or
timezone starts a new occurrence, so a partially completed external effect
could run again under a new key. None of these metadata fences cancel an
external side effect that a handler already performed.

## Run history

Each claim records one run in `pgbase_meta.scheduled_job_runs` (system
migration 74) in the same statement that takes the lease, and completion
records the outcome on that row together with the fenced job update. The
administrator console shows it in the job detail pane.

```http
GET /api/admin/jobs/{id}/runs?limit=25&cursor=...
```

Administrator session only (same-origin read, like `GET /api/admin/jobs`). The
route is mounted when the job worker exists. Results are newest first:

```json
{
  "items": [
    {
      "id": 42,
      "status": "failed",
      "attempt": 2,
      "scheduledAt": "2026-11-24T00:00:00Z",
      "startedAt": "2026-11-24T00:00:01.2Z",
      "finishedAt": "2026-11-24T00:00:03.7Z",
      "durationMs": 2500,
      "error": "upstream returned 503"
    }
  ],
  "nextCursor": "...",
  "retention": { "maximumRuns": 1000, "maximumAgeSeconds": 2592000 }
}
```

- `limit` is 1–100 (25 when omitted). `nextCursor` is present only when more
  runs exist; pass it back unchanged as `cursor`. Cursors are signed with the
  token secret, bound to the job, and carry the last run id, so retention can
  only shorten a traversal.
- `status` is `running`, `succeeded`, `failed`, `superseded` (the handler
  returned after its lease expired or was replaced; the job state was not
  changed by it) or `abandoned` (still recorded as running, but its lease
  expired or was replaced; derived on read). A later report from an abandoned
  worker turns the row into `superseded`.
- `attempt` counts retries of one occurrence (1 for the first try).
  `scheduledAt` is the occurrence the run belongs to. `finishedAt` and
  `durationMs` are absent while a run is in progress or abandoned.
- `error` is the handler's error text, truncated to 1,024 characters like the
  job's last error. Handlers return only an error, so a successful run carries
  no result body.
- Errors: 400 `invalid_request` for a malformed id, `limit`, repeated or
  unknown parameter, or a forged, foreign or other-job cursor; 404
  `job_not_found` for an unknown job.

History is bounded like the request log: `Operations.JobRunRetention` removes
runs that started longer ago (30 days when zero) and
`Operations.JobRunMaximumRows` keeps the newest runs per job (1,000 when zero).
Pruning runs after each completion and is best effort: a failed prune is
logged and retried by the next completion. Changing a schedule, disabling or
unregistering a job keeps its history.
