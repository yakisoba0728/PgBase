# Security continuation evidence

See [final-security.md](../../final-security.md) for findings, call-boundary coverage, compatibility changes and limitations. These artifacts were produced by the real security/backend agents in disposable local fixtures. They are separate from final integrated CI.

## Source identity

- Incoming remote source: `b376e36697674544be821be0290e24916d81da7d`.
- Security runtime commits in order: `fc5f347e`, `29272aff`, `a002599c`, `4730e549`.
- Tested final security runtime: `4730e549635ab11f7721c55677923440a3d5cfcf`, tree `0149819660f02f2ab0c2d0f113a29145d194c17e`.
- Separately tested backend runtime: clean detached `58c1038c9055891f13f7dc13491920e676ca4ece`, tree `9845fe3bde247db6c33adea134d302a9da0a7004`, including panic/savepoint changes `35f187b3` / `e8d23fec` and schema 68. This is an independent cross-review target, not the security worktree's runtime identity or the final integrated PR HEAD.
- Later report/evidence commits change no runtime files. GitHub integration may map these local commits to new remote commit IDs; the root workstream records that mapping.

The raw failure log predates the first two fixes. `metadata-failing-before.log` was produced after extracting private Pack/Unpack helpers with a budget argument, while their public behavior and old payload-only accounting were unchanged. Those helpers let the regression use a few KiB instead of decompressing eight GiB. The first and final tests use the same reduced budget and archive fixture. The complete-stream correction is the additional LimitedReader/sentinel and matching writer bound in `4730e549`.

## Actual executions

Go was 1.26.11, linux/amd64, `GOMAXPROCS=2`. PostgreSQL cross-review fixtures used a fresh local 17.9 cluster with TCP-only access because this executor prohibits Unix-domain sockets. PostgreSQL test databases were created and removed by `internal/testutil`; the private cluster was stopped and removed at the end of each command. No production or persistent application data was used.

| Artifact | Command / meaning |
| --- | --- |
| `failing-before.log` | Initial signed OIDC and invalid trailing-gzip-member regressions failed before correction; includes nine invalid OIDC acceptances and the fully drained invalid archive member. |
| `metadata-failing-before.log` | `go test ./internal/backup -run 'TestArchiveExpandedBudget' -count=1 -v`; metadata and exact/over-budget cases against old payload-only accounting, exit 1. |
| `oidc-race.log` | `go test -race ./internal/userauth -run 'TestOIDC\|TestHTTPOAuthProvider' -count=1 -v`; follow-up OIDC implementation, exit 0. |
| `package-race.log` | `go test -race ./internal/backup ./internal/userauth -count=1 -v`; all untagged tests in both affected packages on final security runtime, 70 top-level / 177 total pass events, zero skips. |
| `independent-security-race.log` | Backend agent's independent `go test -race ./internal/backup ./internal/userauth -run '^(TestArchive\|TestPack\|TestUnpack\|TestOIDC)' -count=3 -v`; copied byte-for-byte from its produced log after the actual run. |
| `independent-jobs-race.log` | Security agent's independent `go test -race -tags=integration ./internal/jobs -run 'TestJobManagementFencesExpiredHandlerCompletion\|TestScheduleCannotReplaceAnActiveClaim\|TestExpiredCompletionWithoutManagementCannotRenewItsAuthority' -count=1 -v`; `d66f3d28` + `90d61de7`, three top-level / 15 total pass events. |
| `independent-realtime-retention-race.log` | Security agent's independent `go test -race -tags=integration ./internal/realtime -run '^TestRetention' -count=1 -v`; `6d0299c0`, three passes. |
| `independent-typescript-realtime.log` | `node --test test/realtime-lifecycle.test.mjs test/sse-lifecycle.test.mjs`; SDK `97e7f7e` + `440466b2`, Node 24.22.1, four passes, zero skips. |
| `independent-dart-realtime.log` | `dart test test/realtime_lifecycle_test.dart --reporter expanded`; SDK `97e7f7e`, Dart 3.13.3, two passes. |
| `independent-transaction-panic-worktree-race.log` | Initial successful panic/savepoint race run in the backend working tree: three top-level / nine total passing events. Queue/schema-68 work was uncommitted there, so this log is retained without assigning a complete immutable runtime identity. Superseded for qualification by the pinned rerun below. |
| `independent-backend-58c1038c-race.log` | Clean detached backend commit, exact command below; eight top-level cases repeated three times, 24 top-level / 42 total passing events, zero skips/failures. Includes panic/savepoint, schema upgrade/concurrent writer, queue transaction/order and checksum regressions. |
| `baseline-artifact-recheck.json` | Independent parsing and SHA-256 checks of the **previous** checked-in full-source CodeQL/image ZIPs. This is neither a fresh scan nor proof that the latest runtime/image has the same finding set. |

These targeted independent executions complement the implementation agents' broader runs. They do not represent PostgreSQL 16/18 integration, external OAuth providers, all native-library call paths, whole-repository tests, or browser E2E. No test was added as a skip or relaxed to obtain success. The first two PostgreSQL startup attempts failed before tests because of the Unix-socket restriction; the TCP-only isolated execution succeeded without changing test assertions.

## Fresh candidate scanner reconciliation

[ci-43795108138](ci-43795108138/README.md) records the subsequent independent reconciliation of all 24 Go / one JS finding and 32 image records on published candidate `9934a687`, checked-out merge `69fa5e77`. Both Git objects and equivalent local `1e5e3d39` have tree `0e4d7a35239bab166fabdb881f9ec3f5e12b01e7`. Original ZIPs and GitHub API metadata are retained by the integration workstream in [final-qualification/attempt-1](../final-qualification/attempt-1/); their size/API SHA-256 comparisons are included in the recheck JSON.

The follow-up includes raw independently fetched job logs compressed without changing their decoded UTF-8 text, exact executed-query counts and skipped-step facts, per-location source classifications, changed-flow/guard-file Git blobs, actual certificate/timezone byte checks and all remaining native dependency risks. It is neither an all-platform source analysis nor an overall Ready judgment. Full CI on this attempt had other failing gates; subsequent test-only corrections require their own CI evidence.

The pinned backend cross-review used the following exact selection, with `PGBASE_TEST_ADMIN_URL` pointing to the disposable TCP-only PostgreSQL 17.9 cluster:

```sh
GOMAXPROCS=2 go test -race -tags=integration \
  ./internal/lifecycle ./internal/recordops ./pgbase \
  ./internal/migrate ./internal/realtime ./migrations/system \
  -run '^(TestRunReportsOperationPanicToAfterWithoutSwallowingIt|TestPanickingMutationReportsRollbackInsteadOfSuccess|TestTrustedSavepointRollbackFailurePreventsOuterCommit|TestOutboxQueueUpgradePreservesStateAndConcurrentOldWriter|TestOutboxQueueAdmissionIsAtomicAndAcceptsLegacyDeliveryInsert|TestPendingQueuePreservesRecordOrderAcrossDeadLetterAndConcurrentWorkers|TestPendingQueueIdentityCommitsWithItsOutboxEvent|TestHistoricalMigrationChecksumsRemainImmutable)$' \
  -count=3 -v
```

`manifest.json` records file hashes and the tested runtime identity. Failure logs are retained rather than overwritten by successful output.
