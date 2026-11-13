# Local scheduled jobs

Jobs are registered by the Go program that starts PgBase. Set
`PGBASE_JOBS_ENABLED=true` and add `ScheduledJob` entries before calling
`pgbase.New`. Schedules may use `@every <duration>` from one second through 24
hours or five-field cron with an IANA timezone. The runner uses PostgreSQL's
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
- With `PGBASE_JOBS_ENABLED=true`, JavaScript jobs from `PGBASE_JS_DIR` are
  registered as well.

Without either, `GET /api/admin/jobs` returns `{"items":[],"workerEnabled":false}`
and the update and run routes are not mounted. That empty list is intended.

The default handler deadline is 30 seconds and the lease is one minute. The
fixed relationship keeps cooperative handlers from being reclaimed while they
are still running. It does not make non-cooperative handlers exactly-once.
