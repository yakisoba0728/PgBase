# Final backend evidence — 2026-11-16

This directory preserves the Backend agent's local reproductions, fixes and
repeated outbox-history experiment for PR #148. It does not replace final CI
on the integrated branch. The source coverage and remaining risks are in
[the backend report](../../final-backend.md).

## Artifacts and provenance

- [raw.zip](raw.zip): 27 original files, including every failed reproduction,
  the killed test process, intermediate compatibility-test failures, passing
  reruns, all eight A/B logs, the runner, instrumentation patches and complete
  disposable PostgreSQL server/initdb logs. No benchmark binaries are included.
- [manifest.json](manifest.json): each archive entry's SHA-256 and size, test
  outcomes and source relationship; archive checksum and readable-artifact hashes.
- [outbox-abba.json](outbox-abba.json): original repeated-run manifest, binary
  hashes, order/timestamps, all run-level results and repeat-mean intervals.
- [outbox-plan-summary.json](outbox-plan-summary.json): extracted execution times,
  shared-buffer counts and relation/index nodes. The complete EXPLAIN JSON is
  preserved in the diagnostic logs inside the archive.
- [environment.json](environment.json): observed Go/PostgreSQL versions,
  PostgreSQL configuration and startup arguments, pool session configuration,
  and explicit limitations of resource and host-isolation evidence.

The Backend branch started at `b376e36697674544be821be0290e24916d81da7d`.
Its last runtime commit is `58c1038c9055891f13f7dc13491920e676ca4ece`, followed
by test-only `68f008632565bff495205be80e77071c39a75528`. Earlier regressions ran
while their implementation edits were still uncommitted; the manifest records
which runtime changes were present without inventing pre-run immutable hashes.
The independent Security execution later used a clean detached `58c1038c`
checkout. Final integration includes other agents' runtime changes and needs
its own complete gates.

## Outcomes, including unsuccessful executions

| Raw file | Observed outcome |
| --- | --- |
| `backend-jobs-before.log` | Ten expired management/completion cases and the active-lease Schedule case failed. |
| `backend-jobs-after.log` | Full jobs suite passed race ×3, 9.586 s. |
| `backend-reproductions-before.log` | Lifecycle panic, record panic/rollback outcome, retention floor and trusted nested rollback reproductions failed. |
| `backend-small-fixes-after.log` | Complete jobs/lifecycle/Realtime suites passed race ×3, 8.306 / 1.134 / 18.778 s. This adds direct expiry without management. |
| `backend-transaction-fixes-after.log` | Record hook/transaction/batch tests passed race ×2, 30.680 s. The broad pgbase filter also selected an existing five-MiB editor fixture; that process ended with `signal: killed` at 101.974 s. No assertion/race diagnosis or kernel evidence established the cause. |
| `backend-trusted-fixes-after.log` | Exact five trusted/external-hook/JS transaction tests passed race ×3, 15.870 s. This does not replace the complete editor/full-race gate. |
| `backend-independent-security-review.log` | Independent archive/OIDC selection passed race ×3, backup 2.019 s / userauth 2.083 s; 42 top-level and 210 total pass events, zero skips. |
| `backend-queue-regressions.log` | Five queue/upgrade/checksum tests passed race ×3, migrate 2.779 s / Realtime 2.725 s / system 1.017 s. |
| `backend-schema68-suite.log` | Full migrate/Realtime/system suites passed race, 9.922 / 7.295 / 14.781 s. Doctor failed its stale latest-schema-67 test guard; the complete doctor process was 68.823 s. |
| `backend-schema68-compatibility.log` | Doctor passed race ×3. The added schema-67 AutoMigrate rejection test exposed a fixture's incorrect array-index/version assumption; the three repetitions failed that assertion while runtime rejected the incompatible history. |
| `backend-schema68-compatibility-after.log` | The corrected doctor/application tests passed race ×3, 8.474 / 11.320 s, preserving the stronger schema-67 rejection checks. |
| `backend-history-before.log`, `backend-history-after.log` | Sequential diagnostic benchmarks and actual EXPLAIN plans; all tails retained. These diagnose the history scan and are separate from the interleaved experiment. |
| `outbox-abba/01-A.log` through `08-A.log` | Eight successful processes, 1,000 measured samples each, ordered A/B/B/A/A/B/B/A. Go's one-sample calibration records also remain in the raw logs and are excluded from the 1,000-sample summaries. |

All raw test selections in this archive have zero skip events. The manifest
counts top-level and nested pass events separately; repeated executions are
not reported as distinct test cases.

## Repeated history experiment

Variant A is `6d0299c0` with the old claim/discovery path and schema 67. Variant
B is `58c1038c` with transactionally admitted pending deliveries and schema 68.
Both apply the captured seven-line raw-sample instrumentation after the timer
stops. The binaries were built before the timed sequence. This comparison
includes the complete queue architecture change; it does not isolate a single
SQL index independently from the enqueue design.

Every repetition creates a fresh disposable database containing 100,000
delivered outbox events and durable delivery markers. Its replay-event table is
empty, modeling history after replay retention. Both history tables receive
`VACUUM (ANALYZE)`, followed by ten warm-up polls. The measured operation is one
closed-loop `Service.Deliver` call that finds no work. Fixture construction and
cleanup are outside latency samples. Four repetitions per variant were used.

| Repetition | Variant | p50 ms | p95 ms | p99 ms | Completed idle polls/s |
| ---: | :---: | ---: | ---: | ---: | ---: |
| 1 | A | 24.647 | 39.747 | 120.085 | 34.65 |
| 2 | B | 0.161 | 0.215 | 0.281 | 6,349.30 |
| 3 | B | 0.161 | 0.281 | 0.502 | 5,621.59 |
| 4 | A | 24.675 | 38.479 | 117.546 | 33.98 |
| 5 | A | 25.859 | 39.380 | 59.157 | 34.76 |
| 6 | B | 0.146 | 0.281 | 0.506 | 6,058.17 |
| 7 | B | 0.144 | 0.268 | 0.441 | 5,985.37 |
| 8 | A | 24.183 | 36.076 | 128.831 | 36.09 |

The original legacy discovery plan reads all 100,000 source rows and all
100,000 delivered markers to return zero. The new claim uses the empty pending
partial index. This explains the repeated difference without attributing it
to the much smaller allocation change. The JSON manifest computes each
percentile at sorted index `floor((n - 1) × percentile / 100)` and throughput
as sample count divided by the sum of sampled durations. Its t95 intervals
describe means of four independent fixture repetitions; they do not treat
8,000 requests as independent experiments.

The host was shared, with `GOMAXPROCS=2`, nine reported CPUs, PostgreSQL 17.9,
128 MiB shared buffers and 100 maximum connections. No test/build was launched
by this agent during the eight timed runs. Other host activity was not fully
recorded, and cgroup hard limits were unavailable. All results therefore have
that limitation. The experiment does not quantify coordinated omission,
enqueue write overhead, active-event delivery capacity, fan-out, long-term
index bloat, global publication-lock contention, or database growth. The
diagnostic began without a production SLO gate; no capacity acceptance target
has been invented after seeing its results.

## Reproduction commands

Use an isolated PostgreSQL 16/17/18 instance where the synthetic test admin may
create/drop disposable databases. This local evidence used 17.9. Set
`PGBASE_TEST_ADMIN_URL` to that instance; the tests allocate random database
names and drop them on completion. Do not point it at an operational database.
Use Go 1.26.11 and ensure its binary is first in `PATH`.

The focused queue/checksum gate was:

```sh
go test -tags=integration -race \
  ./internal/migrate ./internal/realtime ./migrations/system \
  -run '^(TestOutboxQueueUpgradePreservesStateAndConcurrentOldWriter|TestOutboxQueueAdmissionIsAtomicAndAcceptsLegacyDeliveryInsert|TestPendingQueuePreservesRecordOrderAcrossDeadLetterAndConcurrentWorkers|TestPendingQueueIdentityCommitsWithItsOutboxEvent|TestHistoricalMigrationChecksumsRemainImmutable)$' \
  -count=3 -v
```

The corrected compatibility gate was:

```sh
go test -tags=integration -race ./internal/doctor ./pgbase \
  -run '^(TestRunRequiresADeclaredCompatibleHistory|TestPublicIdentityArtifactStartsOnlyOnCompleteHistory)$' \
  -count=3 -v
```

For a repeated performance run, extract the archive into a separate artifact
directory; create two isolated worktrees at A and B; apply the matching
instrumentation patch in each; build each binary before starting measurements:

```sh
go test -tags=integration -c -o /absolute/artifact/path/before.test ./internal/realtime
go test -tags=integration -c -o /absolute/artifact/path/after.test ./internal/realtime
```

Run each build command in its corresponding worktree. Then, with the same
disposable PostgreSQL instance and exported `PGBASE_TEST_ADMIN_URL`:

```sh
GOMAXPROCS=2 python3 /absolute/artifact/path/outbox-abba/run.py \
  --before /absolute/artifact/path/before.test \
  --after /absolute/artifact/path/after.test \
  --before-commit 6d0299c0 --after-commit 58c1038c \
  --output /absolute/artifact/path/new-outbox-abba \
  --cycles 2 --iterations 1000
```

Do not overwrite the original evidence directory. `PGBASE_BENCH_EXPLAIN=1`
enables labeled full plans for a separate diagnostic benchmark; the repeated
runner removes that variable so EXPLAIN is outside the timed experiment.

## Independent review boundaries

The Security agent's pinned `58c1038c` cross-review and actual race execution
are documented in its separate `869c1f58` evidence addendum. The QA agent also
executed queue/transaction regressions independently. These are actual separate
reviewers, not merely parallel CI jobs.

Backend separately read and executed the Security archive/OIDC patches through
`4730e549`. It also reviewed QA's later invocation rollback patch `5fc067c9`
and its real-transport regression from the immutable commit. The latter review
was source-only to preserve the measurement window; its execution results
belong to QA. Full integrated race, PostgreSQL version matrix, UI/SDK/browser
contracts, security tooling and release candidate gates remain the lead's
responsibility on the final combined source.
