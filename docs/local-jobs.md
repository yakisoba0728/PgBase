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
