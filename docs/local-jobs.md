# Local scheduled jobs

Jobs are registered by the Go program that starts PgBase. Set
`PGBASE_JOBS_ENABLED=true` and add `ScheduledJob` entries before calling
`pgbase.New`. Only `@every <duration>` schedules are supported, from one
second through 24 hours, and the runner uses PostgreSQL's UTC clock.

Run the local example after setting the normal database and token environment:

```sh
PGBASE_JOBS_ENABLED=true go run ./examples/jobs
```

The runner is at-least-once. A process can crash after an effect completes, or
a handler that ignores its context can outlive its lease and overlap a retry.
Handlers must honor their context. Use `JobRun.RunKey()` as an idempotency key
at external providers. For effects committed in PostgreSQL, record the highest
`FenceGeneration` for the job and reject older attempts in the same transaction.

The default handler deadline is 30 seconds and the lease is one minute. The
fixed relationship keeps cooperative handlers from being reclaimed while they
are still running. It does not make non-cooperative handlers exactly-once.
