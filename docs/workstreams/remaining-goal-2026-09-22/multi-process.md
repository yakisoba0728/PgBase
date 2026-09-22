# Bounded multi-process acceptance

## Scope

`TestMultiProcessBoundedFailureAcceptance` is a local correctness workload for two independent PgBase operating-system processes sharing one isolated PostgreSQL database. It does not measure production throughput, prove multi-host behavior, or contact an existing installation or external provider.

The test builds the current `cmd/pgbase` binary, reserves two loopback addresses, creates a UUID-named database through `PGBASE_TEST_ADMIN_URL`, and starts two `pgbase serve` child PIDs. Only the first process performs automatic migrations. The test kills only a PID it started and restarts that process on its original address. The standard integration database cleanup terminates remaining connections and drops only the UUID-named test database.

## Workload and checkpoint

The bounded workload performs:

- 32 concurrent record creates, split evenly across both server processes, with unique idempotency keys;
- 12 concurrent creates with one shared idempotency key, accepting the documented contention result (`201 Created` or `409 idempotency_in_progress`), followed by successful replay through each process and an identical record ID;
- a user SSE connection to process one and a record write through process two;
- a forced kill of process one, another durable write through process two, restart of process one, and SSE resume from the last delivered cursor;
- logout through process one, immediate session rejection through process two, and closure of the resumed SSE connection;
- a fresh login and final listing through the restarted process.

The explicit no-data-loss checkpoint requires exactly 35 durable records: 32 unique writes, one shared-key idempotent write, and two writes around the kill window. It also requires the post-restart SSE stream to deliver the write committed while process one was down. This is a bounded correctness guarantee for this fixture and workload only.

## Reproduction

Run against an isolated PostgreSQL administrator URL:

```sh
go test -tags="integration multiprocess" ./pgbase \
  -run '^TestMultiProcessBoundedFailureAcceptance$' -count=1 -v
```

On the shared Linux runner used on 2026-09-22, the passing run reported:

```text
unique_requests=32 idempotent_requests=12
idempotent_status_classes=map[201:1 409:11]
durable_records=35 killed_and_restarted=1
realtime_resumed=1 cross_process_revocation=1
```

The observed `409` responses are the stable `idempotency_in_progress` contention class. Retrying the same request through both processes returned `201` with the one durable record ID. No production correctness defect was found in this acceptance run.
