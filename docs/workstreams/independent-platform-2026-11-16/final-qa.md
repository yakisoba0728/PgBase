# Independent reliability and QA review

## Scope and disposition

This is the work of the separate QA agent, performed after the Backend,
Security, and Frontend agents implemented their changes. It is an independent
review of the boundaries listed below, not an exhaustive audit of every Go
source file, administrator screen, or SDK. Two additional defects were
reproduced and fixed. The selected tests pass, with the limitations and original
failures preserved in [the raw evidence](evidence/final-qa/README.md).

The QA worktree began at `b0960a39`. UI correction
`7451fe70208647537fb2583b7f29dbcf890097cb` includes rebuilt assets. The independent
backend tests ran at `a1acd15e`, containing the same panic, savepoint, and schema
68 changes as their originating commits `35f187b3`, `e8d23fec`, and `58c1038c`.
The final QA runtime change is
`5fc067c98ce98254aed464c76378e9da6bde4e57`; only the focused extension cleanup
suite was run after that Go change. The earlier database and migration runs
must not be described as a complete Go suite on the final integrated head.
Cherry-pick hashes on the PR branch can differ; the source changes and original
commits identify the reviewed artifacts.

This report alone does **not** establish Ready status. The final integrated
revision still needs the parent workstream's full CI, candidate-image browser
tests, security scans, and workload qualification. A browser test discovered by
Playwright is not an executed browser test.

## Additional defects reproduced and fixed

### QA-1: old administrator operations still ran completion side effects

The Frontend patch correctly stopped later requests in a multi-step operation
after its administrator session changed. However, `FileField` unconditionally
ran React Query's mutation completion callbacks. The rejected old operation
could still call `onRecordChanged`, which calls `useRecordForm.reload`, save the
old form's draft, and invalidate a replacement session's record/file caches.
It also emitted a global toast. `RealtimeView` likewise stopped queued retries
but still invalidated the global Realtime cache or emitted a toast after an old
retry settled. Unmounting a view does not suppress these mutation callbacks.

Four regression cases failed on the inherited implementation; seven controls
passed. The correction captures the scope when the mutation is invoked and
keeps it in the mutation variables, covering both request execution and later
React Query callbacks. Old progress updates, parent reloads, cache invalidation,
and global notifications are suppressed. A failure in the same administrator
session still refreshes uncertain server state exactly once. The Realtime
completion path checks its original scope before global side effects.

The change preserves the existing visual design. It does not undo a write that
was already sent and committed before the session changed. The regression is
about client lifecycle isolation; these tests do not demonstrate an independent
server authorization bypass.

Evidence: [failing cases](evidence/final-qa/ui-stale-settlement-before.log),
[67 related tests](evidence/final-qa/ui-session-after.log),
[443-test suite](evidence/final-qa/ui-full.log),
[11 final affected tests](evidence/final-qa/ui-final-affected.log), and
[asset reproduction](evidence/final-qa/ui-dist-reproducibility.log).
The full UI run used the final runtime sources. A subsequent test-only type
correction removed two unsupported Testing Library `exact` options; the final
affected tests, typecheck/build, and asset reproduction passed after that
correction. The initial typecheck failure is also retained.

Security independently reviewed immutable commit `7451fe70` and found no
blocker in these changes. That review was source review; it did not duplicate
the QA test executions.

### QA-2: JavaScript invocation cleanup could wait indefinitely for rollback

`extensionHost.rollbackAppTransaction` detached caller cancellation with
`context.WithoutCancel` but imposed no new deadline. A database connection that
accepted the rollback request and then stopped returning responses could keep
`host.Close` waiting indefinitely, holding the invocation's database mutex and
delaying transaction observers and later cleanup.

The regression test uses a real PostgreSQL transaction and pgx connection. It
writes a row, cancels the invocation, and wraps only that disposable connection
to discard subsequent server replies while preserving normal socket deadline
behavior. The inherited implementation exceeded the three-second test watchdog;
the test then closed its own socket to avoid leaving a hung goroutine.

The correction uses the existing shared one-second
`database.BoundedCleanupContext` for rollback. Across three race-enabled runs,
the lost-response cleanup returned in approximately one second, reported the
deadline failure to the transaction observer, discarded the ambiguous pgx
connection, left no committed row, and made a second close harmless. Three
existing extension transaction/object-close controls also passed three times.

This bounds the database rollback phase. It is not a claim that arbitrary
extension callbacks, file closers, or unrelated object operations must all
finish within one second.

Evidence: [before](evidence/final-qa/extension-cleanup-before.jsonl) and
[after with race detector](evidence/final-qa/extension-cleanup-after-race.jsonl).
The Backend agent independently reviewed immutable commit `5fc067c9` and found
no blocker in the cleanup lifetime or real transport regression. That was a
source review; the race executions are the QA evidence above.

## Independent review of other agents' changes

### Panic and borrowed-transaction outcomes

Reviewed lifecycle `Registry.Run`, record mutation hook completion, owned and
borrowed transaction helpers, and the new panic regressions. The direct deferred
hook finalizer observes the panic after SQL cleanup, reports a generic failed
operation without copying private panic details, and rethrows the original
panic. A caller recovering from a failed borrowed savepoint and committing its
other work cannot turn the failed mutation's observer into a success.

The tests inspect durable record, revision, audit, and outbox counts; they do
not rely only on the returned error or on whether a hook was called. The
independent run includes create/update/batch panic cases, borrowed batch and
truncate outcomes, ordinary failed-savepoint behavior, and the transaction
owner's later commit. No blocker was found in the reviewed changes.

### Trusted nested transaction cleanup

Reviewed the replacement of the duplicate trusted savepoint implementation
with `database.InSavepoint`, including its rollback/connection-discard behavior.
The injected failed rollback test retains both operation and cleanup errors,
requires an independent cleanup deadline, prevents the outer commit from
publishing a failed nested write, and checks the physical table afterward. The
existing cross-application transaction-context and nested-panic tests also
passed independently.

### Schema 68 and durable Realtime admission

Reviewed the complete migration SQL, claim query, retry/status paths, source
transaction enqueue trigger, legacy-insert compatibility, upgrade fixture, and
concurrent worker ordering tests. Queue insertion occurs in the outbox
transaction; a failed enqueue rejects the source write. Migration trigger
installation coordinates with an in-flight schema-67 writer before backfill.
Pending/dead ordering identity is populated without rewriting delivered rows.
The pending predecessor index includes dead predecessors, so an operator must
retry an earlier failed version before a later version of that record advances.

Independent race runs covered upgrade with a blocked old writer, preserved
delivered/dead attempt state, atomic rollback of enqueue failure, legacy
event-id-only insertion, uncommitted event invisibility, multiple workers,
same-record ordering, backoff, dead-letter isolation, and retry. The historical
migration checksum test passed separately. No blocker was found in these paths.
This review does not establish production migration time or throughput for an
unbounded pending queue. The Backend report owns the query-plan/performance
measurements.

## Managed-backup browser fixture review

The separate Compose project/origin, database volume, local file target, and
archive tmpfs prevent restoration from changing the ordinary authenticated or
onboarding browser fixtures. The Playwright configuration rejects a backup
origin equal to either other fixture origin; that negative configuration check
was executed independently.

The browser test checks downloaded archive bytes against metadata size and
SHA-256, uploads a second archive identity, modifies/adds records and deletes an
attachment after the snapshot, then restores the uploaded archive. Its final
assertions inspect original values and ETag, the exact record list, exact file
bytes and content type, readiness, and the rendered record screen. It does not
treat a successful job status alone as restored data integrity.

Locally, Playwright discovered 26 tests in four files, including this managed
backup test. No Chromium/browser execution or Docker candidate-image run was
performed by QA. The independent Go managed-backup HTTP test passed twice under
race, but it is a different test and does not replace the browser fixture gate.

## Open-loop harness review

Reviewed all of `pgbase/open_loop_load_integration_test.go`. It schedules
arrivals independently of responses, includes generator/queue delay in intended
arrival-to-validated latency, and retains attempted timeouts in latency samples.
Dropped or unscheduled arrivals count as errors rather than disappearing from
the offered workload. Stable read rows and separate unique mutation targets
avoid creating accidental inter-request dependencies. Page order/filter/relation
checks and post-load owner cursor traversal, physical counts, update versions,
and cross-owner point-read denial provide meaningful correctness checks.

No new source-level blocker was found. The percentile distribution excludes
never-attempted arrivals, which are separately counted and fail the zero-error
gate; it must not be interpreted alone when drops occur. Client and embedded
server share a process, so runtime counters are combined and do not isolate
server RSS or database CPU. This agent did not execute the workload or verify
the 200 RPS / 600-second qualification. Measurements and final workload judgment
belong to the actual final-runtime run, not this harness review.

## Execution record

Go 1.26.11, PostgreSQL 17.9, `GOMAXPROCS=2`, a private loopback-only PostgreSQL
cluster with 128 MB shared buffers, and Node 24.22.1 were used. Each database
cluster was started, tested, stopped, and removed within one execution lifetime.
No production database, actual credential, or deployment was used.

| Check | Actual result | Boundary |
| --- | --- | --- |
| Four UI stale-settlement regressions before fix | 4 failed, 7 controls passed | Expected reproduced defect; retained |
| Related UI lifecycle/API tests | 67 passed, 8 files | Unit/jsdom, not browser |
| Complete UI suite | 443 passed, 78 files | Same final runtime sources; test-only type correction described above |
| Final affected UI tests | 11 passed, 2 files | Exact final affected source/tests |
| Typecheck/build and dist reproduction | Passed | Existing bundle/design preserved |
| Transaction, panic, savepoint, managed-backup race | 14 top-level tests, each twice; 44 pass events including subtests | `a1acd15e`; 0 failed/skipped |
| Queue/migration/retry race | 6 top-level tests, each twice; 12 pass events | `a1acd15e`; 0 failed/skipped |
| Historical migration checksum race | 1 passed | Selected separately after an initial selector matched no test |
| Lost-response cleanup before fix | 1 failed at watchdog | Expected reproduced defect; retained |
| Lost-response cleanup and extension controls after fix | 4 top-level tests, each three times; 12 pass events | Runtime change in `5fc067c9`; 0 failed/skipped |
| Browser configuration | 26 tests discovered; same-origin backup rejected | No browser execution |

The first local PostgreSQL start failed because Unix-domain sockets are denied
by this environment; TCP binding succeeded. The supported TCP-only fixture then
started normally. The initial test-only TypeScript error and the empty checksum
test selection are explicitly recorded in the evidence directory. Neither is
presented as a product test failure or silently omitted.

## Traceable source boundary and remaining gates

| Reviewed boundary | Files or bounded call path |
| --- | --- |
| UI session and API lifecycle | `ui/admin/src/api/http.ts`, `records.ts`, `session.ts`; `app/signout.ts`; `schema/apply.ts`; relevant changed lifecycle tests |
| UI mutations and queues | `collections/FileField.tsx`, `RecordsView.tsx`, `queries.ts`; `ops/RealtimeView.tsx`; `app/limit.ts`; `RecordEditor.tsx` callback and `useRecordForm.ts` reload path; corresponding tests |
| Browser restoration isolation | `tests/e2e/browser-backups.compose.yaml`, `browser-files.compose.yaml`, `deployments/docker-compose.yml`, `.github/workflows/e2e.yml`, `ui/admin/playwright.config.ts`, managed-backup spec |
| Record transaction and panic observation | `internal/lifecycle/registry.go`; `internal/recordops/hooks.go`, `transaction.go`; changed Create/Update/Delete/Batch/Truncate finalizers and their selected regressions |
| Trusted and JS transaction cleanup | `internal/database/tx.go`, pool and pgx rollback behavior; `pgbase/trusted_database.go`; `extension_app_transaction.go`; `extensionHost.Close` and object-transaction finalization boundary |
| Realtime queue and retention boundary | `internal/realtime/outbox.go`, `status.go`, retry/queue tests; complete `000068_realtime_pending_queue.sql`; upgrade and migration checksum tests |
| Load-generator correctness | Complete `pgbase/open_loop_load_integration_test.go` |

The remaining application packages, complete HTTP/authentication surface,
JavaScript capability model, SDK implementations, accessibility across every
screen, browser engines other than the configured Chromium project, PostgreSQL
16/18 behavior, full-process stress/resource exhaustion, and complete security
scan output were not independently re-audited by this QA pass. Other agents'
reports and final CI must be read for their own coverage; they must not be added
to this report's source-review count as though this agent performed them.

Final release readiness must use the final integrated runtime's evidence. In
particular, an unresolved repeatable performance regression or an unexecuted
mandatory restore/browser/security gate cannot be converted into Ready by these
passing selected tests.

## Read-only review of later qualification fixture corrections

The QA agent reviewed these immutable follow-up commits without running tests
or load during the separate field-order performance experiment. No additional
runtime change is approved or qualified by this section.

- `1caaf85f` corrects the open-loop fixture's total-count expectation to the
  existing native API contract. `RecordPage` documents the 10,000 cap;
  `internal/query/expand.go` computes `TotalCapped = total > maximumListTotal`
  and then takes the minimum. The fixture now verifies both values exactly.
  At 120,000 arrivals it still checks 24,256 physical rows, 12,000 creates,
  12,000 updates, and every owner cursor page with exactly 12,128 unique records
  per owner, including 6,000 creates and 6,000 updates. Owner/group/identity,
  final-cursor exhaustion, cross-owner denial, zero errors/drops, and the
  original 250 ms p99 threshold remain enforced. No integrity assertion was
  relaxed. This correction cannot excuse a separate run that exceeds p99.
- `dea3ca96` changes `PGBASE_LOAD_EVIDENCE_DIR` from a package-relative path to
  the absolute checkout workspace. The workflow shell, raw CSV guard, and
  artifact upload now address the same directory even though `go test` runs
  the test binary from its package directory. A successful test must also
  produce 120,000 CSV rows with no request error. `set -euo pipefail` preserves
  test failures and `if: always()` preserves any evidence written on failure.
  Missing per-arrival CSV from an earlier execution remains an evidence gap;
  source review cannot recreate those samples or establish a successful rerun.
- `5ded8e98` strengthens repository policy tests to require all three browser
  backends, their independent projects/ports/origins, administrator setup, all
  three Playwright environment variables, selected-image reuse, and teardown
  in the always-run step. This extends the previous two-backend policy.
- `13db9a43` consumes backup create/upload/restore response bodies immediately
  in the response promise while the UI action completes. Expected statuses and
  all archive, record, ETag, file-byte, readiness, and rendered-UI assertions are
  retained. It adds no skip, tolerance, or retry that could hide a failure.

No source-level blocker was found in these four test/CI corrections. The QA
agent did not independently inspect the cited hosted run's raw logs or rerun
the workloads in this follow-up review; the final CI evidence must state its
own tested revision and outcome.

The agent also independently reviewed the field-order diagnostic protocol
`d996a117`, Python runner, Go observation overlay, and prepared common historical
HTTP harness. Two pre-execution findings were corrected in `8cb52e16`: observer
query errors must remain failures even when the sampling loop has just been
canceled, and an interrupted runner must terminate/reap its active child before
removing disposable worktrees. The corrected overlay retains and serializes
query/final-snapshot errors; the runner preserves an interrupted ledger entry and
keeps worktrees if its child cannot be reaped. `pg_stat_io` is explicitly labeled
cluster-wide. Source review of the corrected artifact found no blocker for the
declared diagnostic experiment. Both arms receive identical instrumentation;
request latencies retain monotonic timing, absolute timestamps allow observation
alignment, and error/count/physical-isolation checks remain intact. The observer
changes the measurement conditions, so this is a separate diagnostic experiment,
not a replacement for the previous historical measurement. QA did not execute
the smoke/full runs or infer a performance cause from this source review.
