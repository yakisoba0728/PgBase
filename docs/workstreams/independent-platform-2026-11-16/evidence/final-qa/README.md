# Independent QA raw evidence

See [the scoped review](../../final-qa.md) for conclusions and exact coverage.
Logs are retained as produced. `manifest.json` supplies SHA-256 hashes and byte
lengths; `environment.json` records the QA runtime revision and toolchains.
`go-results-summary.json` is derived from Go JSON test events; repeated tests and
subtests are counted as events, not unique top-level tests.

## UI

- `ui-stale-settlement-before.log`: inherited runtime plus strengthened/new
  tests; four reproduced failures and seven controls.
- `ui-session-after.log`: eight API/UI lifecycle files; 67 passing tests.
- `ui-full.log`: complete jsdom suite, 443 passing tests in 78 files. A subsequent
  test-only correction removed unsupported `exact` options from two Testing
  Library calls; `ui-final-affected.log` executes those final affected files.
- `ui-build-first-type-error.log`: original two TypeScript errors, retained.
- `ui-build.log`, `ui-dist-reproducibility.log`: corrected typecheck/build and
  exact regenerated bundle comparison.
- `browser-discovery-only.log`: `playwright test --list`, not browser execution.
- `browser-origin-rejection.log`: expected rejection when backup and normal
  authenticated projects use the same origin.

UI commands used the repository's locked dependencies (the dependency tree was
reused from the separate Frontend worktree after lockfile equality was checked).
Vitest used `--maxWorkers=2`; the final two-file confirmation used one worker.
The build commands were `npm run build` and `npm run verify:dist`. Staged rebuilt
assets were compared to a second build before committing them.

## PostgreSQL/race

- `independent-transaction-backup-race.jsonl`: 14 selected top-level tests twice
  under `-race`, 44 pass events including subtests, no fail/skip.
- `independent-queue-migration-race.jsonl`: six top-level queue/upgrade/retry tests
  twice under `-race`, 12 pass events. Its checksum-package selector matched no
  test; this is visible in the original log and was corrected by the next run.
- `migration-immutability-race.jsonl`: actual historical checksum test, passed.
- `extension-cleanup-before.jsonl`: real lost-PostgreSQL-response regression,
  failed the three-second watchdog before the cleanup deadline fix.
- `extension-cleanup-after-race.jsonl`: real transport regression and three
  extension transaction/object-close controls, each three times under `-race`.
  The failed rollback connection was discarded and physical row count was zero.
- `postgres-start-failure.log`: diagnostic retry of the first startup failure;
  TCP bind succeeded but Unix sockets were denied. The final fixture disables
  Unix sockets rather than changing sandbox restrictions.
- `postgres-init.log`, `postgres-start.log`, `postgres-server.log`: successful
  private-cluster setup and server-side logs for the independent main run.

Every PostgreSQL invocation used a new private data directory, port 56535 on
loopback, `shared_buffers=128MB`, and an EXIT trap to stop/remove that exact
cluster. The test process and cluster lived in the same execution lifetime.
`PGBASE_TEST_ADMIN_URL` pointed only to that disposable cluster.

The main Go command shape was:

```sh
GOMAXPROCS=2 go test -race -tags=integration -p=1 -count=2 -timeout=8m -json \
  ./internal/lifecycle ./internal/database ./internal/recordops ./pgbase \
  -run '^(TestRunReportsOperationPanicToAfterWithoutSwallowingIt|TestInTxRollsBackOnPanic|TestSavepointFailurePreservesOwnerTransaction|TestFailedSavepointCleanupCannotLeaveWritesForOuterCommit|TestBorrowedTransactionChecksDatabaseStateAndCallerIsolation|TestPanickingMutationReportsRollbackInsteadOfSuccess|TestBorrowedBatchAndTruncateFollowOwnerOutcome|TestFailedBorrowedBatchRollsBackSavepointAndReportsFailure|TestTrustedSavepointRollbackFailurePreventsOuterCommit|TestTrustedDatabaseOptInAndNestedTransactionsStayBound|TestTrustedDatabaseContextCannotCrossAppAndNestedPanicRollsBack|TestManagedBackupHTTPRestoresAndActivatesFreshTarget|TestManagedRestoreDiscardsProvisionedTargetWhenOpeningFails|TestFailedBackupActivationDiscardsOnlyOwnedTarget)$'

GOMAXPROCS=2 go test -race -tags=integration -p=1 -count=2 -timeout=8m -json \
  ./internal/migrate ./internal/realtime ./migrations/system \
  -run '^(TestOutboxQueueUpgradePreservesStateAndConcurrentOldWriter|TestOutboxQueueAdmissionIsAtomicAndAcceptsLegacyDeliveryInsert|TestPendingQueuePreservesRecordOrderAcrossDeadLetterAndConcurrentWorkers|TestPendingQueueIdentityCommitsWithItsOutboxEvent|TestDeliveryFailureDoesNotStrandTheRestOfTheClaimedBatch|TestDeliveryFailureBackoffDeadLetterAndOperatorRetry|TestMigrations.*)$'

GOMAXPROCS=2 go test -race -count=1 -json ./migrations/system

GOMAXPROCS=2 go test -race -tags=integration -p=1 -count=3 -timeout=5m -json ./pgbase \
  -run '^(TestExtensionHostCloseBoundsRollbackWhenDatabaseRepliesStop|TestExtensionHostCloseDoesNotHoldFileMutexWhileFinalizingObjectTransaction|TestRecordHooksFollowExternalTransactionOutcomeExactlyOnce|TestPocketBaseRunInTransactionUsesInvocationBoundPostgresTransaction)$'
```

The before-fix transport regression used the same single-test name with
`-count=1`. Its socket watchdog exists only in the test to stop the deliberately
failing baseline; it is not a production timeout and did not conceal the failure.
