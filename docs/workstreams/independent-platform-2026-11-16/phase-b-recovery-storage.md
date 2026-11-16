# Phase B recovery: durable variant cleanup

## Candidate and scope

The recovered Storage candidate is `4b49d7c0`, based on published checkpoint
`06938a41117072aa63b872b108652ea3a9e0cd94`. The runtime implementation and schema
69 already existed in that checkpoint and were preserved. This recovery brings
back the unpublished fault and upgrade tests from `3500edf8` (original Storage
commit `a1ee3dba`) and the operator guidance from `1545b1a7`. It does not import
the prior raw logs or claim their results apply to this candidate.

No new runtime defect was confirmed in the bounded Storage review. The existing
uncertainty retention remains deliberate; reducing retention without proof that
remote writes have stopped would lose the safety property.

## Validation performed on this source

All PostgreSQL tests used a fresh disposable loopback PostgreSQL 17.9 cluster,
separate temporary databases, Go 1.26.11, `GOMAXPROCS=2`, package parallelism one,
and normal PostgreSQL durability defaults. PostgreSQL was started, tested and
stopped within the same executor session. No production resources were used.

| Run | Result |
| --- | --- |
| Filestore and schema unit tests with race detector | 38 top-level tests passed; no skips |
| Focused variant/restore/upgrade PostgreSQL integration | 15 top-level tests passed; no skips |
| Full affected packages with PostgreSQL and race detector | Filestore 75 passed, migrate 31 passed, system migrations 38 passed; zero failures |

The full affected run has four pre-existing optional MinIO skips because no
MinIO fixture was configured. Their exact names and the original test output
remain in the evidence manifest. No test was weakened or newly skipped. These
local results do not certify actual S3 fault handling or the final integrated CI.

## Safety evidence

- **Intent exists before Put:** the partial-write fixture queries the committed
  retained intent inside the actual object-store Put. The failure leaves no
  published variant and retains the intent.
- **Publication and deletion races:** the production staging and object-reference
  advisory locks fence cleanup. Eight concurrent cleaners perform one deletion;
  delayed COMMIT and ROLLBACK remain safe after the staging session is terminated.
- **Unknown COMMIT:** a loopback PostgreSQL protocol proxy drops a real COMMIT
  reply after the server emits CommandComplete. The client reports an error while
  the committed reference survives and its object remains readable.
- **Crash/restart and late Put:** a child process is killed after writing actual
  local object bytes; a new pool and service resume cleanup. Separate tests
  terminate both ownership sessions, delete an absent object, then finish a late
  Put. The retained intent survives and a subsequent pass removes those bytes.
- **Bounded request pool:** four simultaneous creators succeed with request pool
  four; exhausting all four connections prevents a fifth publication and respects
  its context deadline. Reservation occurs before acquiring the publishing tx.
- **Deletion failures:** retry state persists even though DrainOne returns the
  storage error. A successful retry physically removes the object, and delayed
  retained work does not block an ordinary due deletion.
- **Integrity:** live referenced original bytes survive cleanup. Schema 68 to 69
  upgrade preserves an ordinary queued deletion, and repeating migrations stays
  compatible at schema 69. A legacy DELETE against an uncertain intent fails with
  SQLSTATE `55000`; storage migration activation is rejected while uncertainty
  exists. Isolated restore detaches source intents and credentials.

The key implementation boundaries are `variantObject`, `reserveObject`,
`DrainOne`, `ObjectMigrationService.activate`, `ResetRestoredStorage`, and
`pgbase_meta.protect_uncertain_object_intent`. Independent QA reviews these
boundaries separately; this author-side report does not substitute for that
review or the coordinator's final CI.

## Remaining limits

An uncertain write retains its tombstone indefinitely and receives an hourly
deletion retry. This tracks potential late bytes but does not prove eventual
remote-write quiescence or bounded tombstone count. Source migration activation
is intentionally blocked by such tombstones. Operator guidance is now in
`docs/storage-settings.md`; do not prune based on a single absent-object result.

An old cleanup worker cannot remove the retained database intent, but it may
repeatedly fail on that row until upgraded. The ten-second Delete context is
cooperative, not a hard timeout for arbitrary custom stores or blocked filesystem
syscalls. The fault tests use real PostgreSQL and local object bytes with explicit
fault injection; production S3 consistency, network and cancellation behavior is
outside the proven boundary.

## Evidence and continuation

`evidence/phase-b-recovery-storage/manifest.json` records exact commands, source
SHA, source-file hashes, timings, skip names and compressed-log hashes. The
`run-storage-recovery-tests.sh` file preserves the actual disposable runner;
adapt its scratch paths and port before reuse. Raw test output and PostgreSQL
logs are compressed alongside it. All three runs exit zero. The next step is
independent QA and a single final integrated CI, coordinated outside this branch.
