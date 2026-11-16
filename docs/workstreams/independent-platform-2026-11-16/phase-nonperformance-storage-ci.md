# Final hosted Storage qualification

**The four PostgreSQL jobs passed on the final source.** The previously skipped
local MinIO coverage is exercised in the hosted fixture. This is a Storage and
database qualification result; performance remains user-owned and the overall
product release decision remains **Not Ready**.

## Exact source binding

| Item | Value |
| --- | --- |
| Tested PR head | `56a881f47d0c27877b91959d7df641c2f603ef03` |
| Tested Git tree | `9b4a2e78540c3737f03f6fcc27398bfaa0424180` |
| Qualification | [43836410887](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887), PostgreSQL jobs from attempt 1 |
| Actual CI checkout (synthetic PR merge) | `0a517baea58bf65e2293f15fb0a9fb54321f7eb7` |
| Author doctor-fix commit with identical tree | `bedac6a9f3da74886e5155bc2eb005e915a0bcd6` |

Each complete job log records the actual synthetic merge in `git log -1`. This
qualification was triggered by `pull_request` with the nonperformance opt-in
label. The merge and PR head share exactly the same Git tree; both identities
are retained. Documentation commits after this source do not change that binding.

## PostgreSQL matrix

| PostgreSQL | Job | Scope | Result |
| --- | --- | --- | --- |
| 16.13 | [133194027537](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027537) | Complete integration packages, MinIO, live SDKs | Success |
| 17.9 | [133194027179](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027179) | Core packages with race, MinIO, live SDKs and administrator consumers | Success |
| 17.9 | [133194027390](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027390) | Application shard with race | Success |
| 18.3 | [133194027244](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027244) | Complete integration packages, MinIO, live SDKs | Success |

The core entries report successful `internal/filestore`, `internal/migrate`,
`internal/backup`, `internal/doctor` and `migrations/system` packages. The
application shard reports successful `pgbase` execution. All four complete logs
contain **zero named Go test skips and zero failing tests**. The declared matrix
places the lower-level Storage tests in the core entries, rather than duplicating
them in the application-only shard. Job-specific PostgreSQL/MinIO resources and
ephemeral loopback ports prevent cross-job fixture ownership.

## MinIO, upgrade and recovery evidence

The four formerly optional local tests are covered on PostgreSQL 16.13, 17.9 core
and 18.3 with the actual isolated MinIO fixture:

- `TestS3StoreAgainstLocalMinIO`
- `TestStorageControllerMinIOSaveRotateRestartAndRollback`
- `TestObjectMigrationLocalToMinIOCutoverAndDelayedCleanup`
- `TestObjectMigrationCancelKeepsCurrentDestination`

The same entries cover fresh schema creation/idempotence,
`TestVariantCleanupUpgradePreservesQueueAndReapplies`, and
`TestVariantCleanupRestoresSchema68BeforeUpgrade`. The latter creates a real
schema-68 dump, restores it into a new database, resets inherited storage before
migration, preserves file bytes, then upgrades/reapplies schema 69. The filestore
tests cover schema-69 retained-intent/source-credential detachment, and
`TestS3FileSnapshotRestoreNewThenStorageResetPreservesIndependentLocalBytes`
covers actual S3-backed snapshot restoration to independent local bytes.

**How individual execution is established:** the existing `testskip` wrapper
suppresses successful per-test output while printing every skip and failure.
Therefore these are not presented as invented individual `PASS` lines. The
source-bound command runs the full selected integration packages without a run
filter and with `-count=1`; the relevant packages succeed, the fixture endpoint
is configured, and no named skips/failures appear. Those combined facts establish
execution of the listed source-defined tests. The evidence manifest includes
their exact definition paths and checksums, command/fixture excerpts and log
hashes.

All three core versions also pass the live public-record, relation/file,
text-record-identity and Realtime SDK contracts, including TypeScript and Dart
Realtime subtests. Detailed SDK unit counts, analysis and administrator-consumer
evidence belong to [the SDK/UI CI report](phase-nonperformance-sdk-ui-ci.md).

## Preserved failed attempt and correction

The superseded [run 43834531061](https://github.com/yakisoba0728/PgBase/actions/runs/43834531061)
tested source `30523ddaf27f8ffebd8b59c5ef802186107a408e`. Its PostgreSQL 16.13,
17.9 core and 18.3 jobs each failed exactly one test:
`TestRunRequiresADeclaredCompatibleHistory`. The doctor fixture still asserted
that the latest migration was 68. The application shard passed; the three
failed core jobs still passed the Storage/migration packages, but their later
SDK steps were not reached and are not claimed as passes.

The correction is test-only: require schema 69, retain the actual schema-67
rejection and add schema-68 rejection. The production doctor contract and all
quality gates remain unchanged. A narrow search found no other obsolete
latest-68 assertion; deliberate schema-68 upgrade/restore fixtures were kept.

Both the author and independent reviewer `recon` executed the exact focused
test using PostgreSQL 17.9 UTF8 and the race detector: **one top-level test and
12 subtests passed**, with no skips/failures. Independent evidence is in
`evidence/phase-nonperformance-doctor-qa/provenance.json`. The published source
has the same Git tree as the tested local correction. The final full PostgreSQL
matrix above then passed, closing the failed gate on the exact final source.

## Evidence and remaining limits

`evidence/phase-nonperformance-storage-ci/manifest.json` records all eight
relevant job outcomes across the failed and final attempts, complete decoded-log
SHA-256 hashes, compact line-numbered excerpts, source hashes, race flags and the
author's focused regression. Raw hosted logs remain available through the
canonical job links. Earlier author Storage evidence remains scoped to its own
source and is not relabelled as the final CI run.

All task-owned local PostgreSQL fixtures are stopped. No local MinIO server was
started, and the earlier attempted build was cancelled. Hosted jobs clean up
their isolated fixtures through the repository workflow.

Production S3/provider interoperability is outside this isolated qualification.
Indefinitely retained uncertain-write intents, migration blocking until proven
remote-write quiescence, and cooperative cancellation limits remain documented
in `docs/storage-settings.md`. No performance test was executed or weakened;
the user's outstanding performance gate still prevents an overall Ready claim.
