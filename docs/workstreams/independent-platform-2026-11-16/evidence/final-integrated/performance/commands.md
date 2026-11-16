# Author commands and limits

The test command runs inside the same shell invocation as an isolated PostgreSQL
17.9 cluster, because this environment does not preserve server network access
across separate invocations. `executed-fixture-wrapper.sh.txt` retains the exact
wrapper used; its absolute toolchain paths identify the local environment.
For manual reproduction use your own disposable PostgreSQL database and set
`PGBASE_TEST_ADMIN_URL`, Go 1.26.11, `GOMAXPROCS=2`, with fsync,
synchronous_commit and full_page_writes enabled.

1. Added the new regression and protocol, with production listener unchanged
   from `23b6c4090e453587325d9dfad8fec6d854f3bee2`:

```sh
bash /workspace/scratch/41607adf79d3/run-auth-postgres.sh test -tags=integration ./internal/schema -run '^TestListenerPreservesRequestPoolCapacityAcrossReconnectAndShutdown$' -count=1 -v
```

This failed as expected: one listener slot reserved, only three simultaneous
request slots before and after reconnect. The first attempt invoked the wrapper
directly and failed with executable permission denied; that setup failure is
preserved separately and does not count as the RED reproduction.

2. Applied the dedicated listener ownership change, then ran the identical
   command: PASS. The request maximum remains four; the schema-only fixture has
   five sessions when all four request slots are held. Listener reconnect,
   cancellation, backend disappearance and exclusive backup gate recovery pass.

3. Focused lifecycle and boundary coverage:

```sh
bash /workspace/scratch/41607adf79d3/run-auth-postgres.sh test -tags=integration -race ./internal/schema ./internal/database ./pgbase -run 'Test(Listener|SchemaApplyAndItsOwnListenerHeartbeat|LongSchemaApplyDoesNotBlockAnotherWriterHeartbeat|IdleSchemaWriterLease|P0IdleWriterLease|CompanionActivation|ActivatedCompanion|Open|PoolStats|CloseContext|BackupGate|DatabasePoolConnectionsParticipate|RunCancellationClosesListenerAndPool|StartRunsAndCloseStopsEveryBackgroundComponent|ConcurrentClose|CloseHonorsDeadline|CloseFirstCaller|CloseDrainsHTTP)' -count=1 -json -timeout=5m
```

PASS: 30 top-level tests + 7 subtests, zero failures and zero skips. The read-only
`full_page_writes` diagnostic key was added afterward, then compiled/checked by:

```sh
go vet -tags=integration,load ./internal/schema ./pgbase
```

Vet PASS. That load-only settings field is also subject to the final source-bound
600-second run. No load gate, test threshold or PostgreSQL setting was modified.

Historical intent: deployment docs formerly counted the listener inside the four
pooled sessions. This task expressly authorizes one additional dedicated listener.
This is an explicit budget separation, not a newly discovered connection leak,
not an isolated speedup estimate, and not proof that the historical 547 losses
were caused solely by the listener. The final sustained gate remains required.

The PgBase app also holds an existing backup writer-gate session. Under the final
diagnostic test, total expected database sessions are four pooled requests plus
one schema listener plus one existing backup gate plus one existing observer,
seven total (six returned by the sampler which excludes itself). Any additional
transient coordination sessions must be counted if observed. Historical total was
six; the runtime change adds exactly one persistent listener session.
