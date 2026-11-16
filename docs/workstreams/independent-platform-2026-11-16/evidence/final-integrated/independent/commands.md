# Independent focused execution commands

Toolchain: Go 1.26.11 and PostgreSQL 17.9. `run-disposable-postgres.sh` is
the exact local wrapper used, copied byte-for-byte after execution from
`/workspace/scratch/41607adf79d3/run-auth-postgres.sh`. It uses a new UTF8
loopback-only test cluster and per-test disposable databases, then stops and
removes that cluster in the same invocation. Paths identify this environment;
replace only tool/cache paths when reproducing elsewhere.

RED source: b2c6b223ef65232247baecd00763845baf43efa0, with only
`internal/schema/listener.go` restored from starting23b6c409. The new test
remains intact. The original listener was then restored to the candidate.

```sh
git show 23b6c409:internal/schema/listener.go > internal/schema/listener.go
bash run-disposable-postgres.sh test -tags=integration ./internal/schema \
  -run '^TestListenerPreservesRequestPoolCapacityAcrossReconnectAndShutdown$' \
  -count=1 -json -timeout=2m
git restore --source=b2c6b223 -- internal/schema/listener.go
```

GREEN source: combined ff5cd9ac3188923400d26297280faa87839b590d, tree
1d30798382078731b9442e4a42aadb0a9d328a34, with no tracked modifications.

```sh
bash run-disposable-postgres.sh test -race -tags=integration \
  ./internal/schema ./internal/database ./pgbase \
  -run '^(TestListener.*|TestSchemaApplyAndItsOwnListenerHeartbeatCannotDeadlock|TestDatabasePoolConnectionsParticipateInBackupGate|TestBackupGate.*|TestConfigureSession.*|TestParsePoolConfig.*|TestOpen.*|TestCloseContextHonorsDeadlineWithBorrowedConnection|TestRunCancellationClosesListenerAndPool|TestStartRunsAndCloseStopsEveryBackgroundComponent|TestCloseDrainsHTTPBeforeCancellingBackgroundWork)$' \
  -count=1 -json -timeout=5m
```

Raw JSONL/stdout, stderr and explicit command exit statuses are retained.
The expected RED failure is separate from GREEN acceptance.
