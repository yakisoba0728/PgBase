# Final backend follow-up — 2026-11-16

This is the backend agent's evidence ledger for the follow-up to PR #148. The
starting commit was `b376e36697674544be821be0290e24916d81da7d`. It records the
boundaries actually examined and separates reproduced defects, source findings,
and remaining work. It is **not a claim of a complete repository audit or release
qualification**. The integration lead owns the final integrated runtime and CI
decision.

## Reproduced job lease defect

An expired handler could complete after an accepted administrator edit and
replace the new schedule, enabled state, or requested next run. Management
checked lease expiry but retained the old token and generation; completion
checked those retained values without checking expiry. Explicit `Schedule` also
replaced a live claim.

Commit `d66f3d28` makes accepted management operations revoke the old token and
increment the generation atomically. Completion requires an unexpired lease as
well as the matching token and generation. `Schedule` now reports
`ErrNotManageable` while a lease is active, like the other management operations.
This is a deliberate behavior change for embedders.

The integration regression uses direct SQL expiry, not sleeps: configure,
disable, explicit schedule, request run, and unregister are each followed by a
delayed success and failure. It compares the complete durable row before and
after completion. All ten cases failed before the fix, as did the live-claim
`Schedule` test. The complete jobs integration suite passed with `-race -count=3`
on Go 1.26.11 / PostgreSQL 17.9, in 9.586 seconds.

An operator edit preserves the outstanding occurrence's `attempts` and
`last_scheduled_at`. Therefore a manual request to retry failed work still has
the same occurrence key; it does not manufacture a new external-effect identity.
The next successful scheduled occurrence receives a new key. Generation fences
protect PgBase's job metadata, not an external service: handlers must still make
external effects idempotent or apply their own generation check.

Independent review: the Security agent read the immutable job patch and its
claim/complete/management callers and found no blocking issue. Its requested
expiry-without-management regression is committed as `90d61de7`; the complete
jobs suite passed again with race ×3 (8.306 seconds). The Security agent also
independently executed the three job management/fencing cases with race.

## Reproduced transaction and observer defects

All four defects below failed in actual tests before their fixes. The raw
failing log is retained, including the rolled-back mutation that emitted a
success hook and the failed nested savepoint whose outer owner committed a
partial write.

| Boundary | Defect and correction | Regression |
| --- | --- | --- |
| `lifecycle.Registry.Run` → operation → `After` | A panic left the named error nil, so `After` received success. `35f187b3` reports a sanitized failure and re-panics the original cause. | Original panic preserved, one failure observation, no panic payload in the observer's error. |
| Record Create/Update/Batch → transaction/savepoint → mutation hooks | A validator panic rolled back SQL, but the outer hook defer saw success; a recovering borrowed owner could commit unrelated work and incorrectly report mutation success. `35f187b3` records the operation failure before final outcome handoff. | Six owned/borrowed create/update/batch cases verify data/revision/audit/outbox rollback and exactly one failure observation. Broader record hook/transaction/batch tests passed race ×2 in 30.680 seconds. |
| Trusted SQL nested transaction → savepoint rollback | A duplicate helper ignored rollback failure and had no cleanup deadline. `e8d23fec` reuses `database.InSavepoint`, including its bounded cleanup and connection invalidation. | Injected rollback failure cannot be followed by an outer commit; original and cleanup errors both survive. Exact trusted/external-hook/JS transaction gates passed race ×3 in 15.870 seconds. |
| Realtime retention → global cursor floor | Selecting any expired sequence advanced the floor across an earlier unexpired event after a clock correction. `6d0299c0` deletes only a bounded, fully expired leading prefix. | An unexpired first sequence followed by an expired second sequence remains replayable until the whole prefix expires. Backlog catch-up and canceled lock wait also pass. |

A broader pgbase race filter selected the existing five-MiB editor HTTP/batch
fixture and the process was killed after 101.974 seconds, before it reached the
new trusted transaction tests. There was no assertion or race report. The raw
failure is retained; its cause is not attributed without kernel/resource
evidence. The exact tests above were then selected and passed. This does not
substitute for the integration lead's final full race gate.

## Realtime scaling correction and measured comparison

The original worker pruned at most 1,000 events per minute: a source-level
cleanup ceiling of 16.7 events per second per worker. Raising the batch size
alone would have lengthened the transaction without removing the scheduling
bottleneck.

`6d0299c0` retains 1,000-row transactions, gives each a one-second deadline plus
the existing bounded rollback cleanup, and continues one batch per delivery
poll while an expired backlog remains. The query reads only the first 1,000
sequence rows and advances the floor only over a fully expired prefix. The
2,501-expired-event fixture catches up in three passes while publishing a new
event on each pass. Tests also verify clock-regression cursor preservation and
canceled lock wait recovery. A future-dated leading event intentionally delays
later cleanup to preserve the replay window. The full Realtime suite passed
race ×3 before the queue migration, then again with schema 68.

Actual `EXPLAIN (ANALYZE, BUFFERS)` confirmed a second bottleneck: every idle
delivery poll anti-joined lifetime outbox/delivery history, reading all 100,000
source rows and all 100,000 delivered markers to return zero events. Across
three 100-iteration diagnostic repetitions, p50 rose from 0.51–0.60 ms at 1,000
delivered rows to 24.45–38.36 ms at 100,000. The legacy query's measured execution
times at 100,000 rows were 32.9–47.2 ms. These sequential measurements had noisy
tails, all of which are preserved; they are not presented as an isolated tail
latency experiment.

Commit `58c1038c` installs migration 68's database-owned enqueue trigger and
replaces both polling discovery and historical predecessor lookup with the
indexed pending queue. Source mutation, audit, outbox, and queue admission
commit or roll back together. A statement trigger admits bulk inserts without
an application round trip per record. Older writers using the outbox-only SQL
still enqueue atomically; a compatibility trigger resolves metadata for old
`event_id`-only delivery inserts. Dead or leased predecessors remain eligible
to block later versions of their record. Delivered identity markers remain
durable and cannot be revived by the retry API.

Migration 68 acquires its DDL locks and backfills under one migration
transaction. It preserves retry/dead/delivered state and populates ordering
metadata only for pending/dead historical markers. A concurrent old writer
test waits for the actual PostgreSQL lock, commits that writer, and verifies
that its event is backfilled. Separate tests verify commit visibility,
rollback on enqueue failure, legacy delivery insert compatibility, four-worker
ordering after a dead-letter retry, and migration idempotence. The five focused
queue/checksum gates passed race ×3. Full migrate/Realtime/system suites passed
race; their reported times were 9.922, 7.295, and 14.781 seconds.

The first full doctor run caught a stale test guard pinned to schema 67. Adding
explicit schema-67 rejection cases also exposed a test assumption that the
migration array index equalled its version despite historical version gaps.
Commit `68f00863` fixes the expectations without weakening them. Doctor and
application-start compatibility gates then passed race ×3 (8.474 and 11.320
seconds), including rejection under `AutoMigrate` without changing history.
Both failing logs remain in the evidence archive. Ordinary startup still
requires the exact declared schema; SQL writer compatibility does not authorize
an old binary to serve schema 68.

On the same 100,000-row diagnostic fixture, the new poll's p50 was
0.130–0.166 ms across three repetitions, and its actual plan uses pending
indexes rather than scanning historical rows. Allocation changed modestly from
about 5,311 bytes / 46 allocations per poll to 5,031 bytes / 41 allocations. The
latency improvement comes from eliminating database history work, not from
claiming that a small allocation reduction is a throughput result.

An interleaved A/B/B/A follow-up uses precompiled binaries, two cycles, fresh
100,000-row fixtures per repetition, 1,000 measured polls, ten warm-up polls,
and `VACUUM (ANALYZE)` on both history tables. Every per-poll nanosecond sample,
binary digest, observed resource setting, order, and repeated-run statistic is retained in
the evidence manifest. The binary sources are `6d0299c0` (old claim SQL/schema
67) and `58c1038c` (queue/schema 68), with the same measurement-only raw-sample
instrumentation patch. All eight executions completed successfully, retaining
8,000 measured samples plus Go's separate calibration samples.

| Metric | A: old discovery, mean of four repetitions | B: pending queue, mean of four repetitions |
| --- | ---: | ---: |
| p50 | 24.841 ms (t95: 23.703–25.979) | 0.153 ms (t95: 0.139–0.167) |
| p95 | 38.421 ms (t95: 35.793–41.048) | 0.261 ms (t95: 0.211–0.311) |
| p99 | 106.405 ms (t95: 55.697–157.112) | 0.433 ms (t95: 0.265–0.600) |
| Completed idle polls/second | 34.87 (t95: 33.46–36.27) | 6,003.61 (t95: 5,527.35–6,479.86) |

The intervals are Student-t intervals for the mean of four run-level results,
not intervals treating requests as independent repetitions. The individual
old p99 values ranged from 59.157 to 128.831 ms; the new values ranged from
0.281 to 0.506 ms. Four repetitions on a shared host do not precisely estimate
production tail latency. They do show a large, repeated reduction in this
particular history-dependent polling cost, consistent with the actual plans.
The experiment was closed-loop and did not correct coordinated omission.
It is useful for comparing one completed idle operation, not predicting queued
response time under a specified arrival rate.

This experiment measures an idle worker poll, not HTTP capacity, mutation
throughput, fan-out, or long-term storage growth. Upgrade still scans existing
history once and creates an index while holding migration locks. Exact
administrator `Status` counts still scan historical markers. Replay cleanup
does not expire append-only audit data, outbox payloads, or delivery identity
markers; deployments need explicit storage capacity planning. The migration's
enqueue write overhead, active-event throughput, dead-backlog behavior under
sustained load, index bloat, and the global publication lock's capacity are not
measured by this experiment. The first sequential runs were diagnostic, not a
predeclared production SLO gate; the interleaved follow-up tests the observed
history-scan explanation without inventing a post-result capacity target.

Independent source and execution review: the Security agent used a clean
detached checkout of `58c1038c9055891f13f7dc13491920e676ca4ece` on PostgreSQL
17.9 / Go 1.26.11. Its eight selected cases passed race ×3, with 24 top-level
and 42 total pass events and no skips. The scope includes panic outcomes,
failed savepoint cleanup, migration locking against an in-flight old writer,
atomic queue admission, dead-predecessor ordering, committed visibility, and
historical checksums. Its evidence and review are committed as `869c1f58`.
The separate QA reviewer also exercised six queue/transaction cases with race
×2 and the historical checksum gate. Neither review reported a blocker.

## Examined boundaries and remaining coverage

The table is a source-review ledger, not a checklist of modules declared
complete. A path listed here means its cited boundary was traced; it does not
mean every other file in that package was reviewed.

| Examined source | Boundary / result | Remaining verification |
| --- | --- | --- |
| `pgbase/app.go`, `background.go`, `app_lifecycle_hooks.go` | Start/Run/Close state, HTTP drain before background cancellation, shared shutdown budget, background runner failures, readiness probes. | Independent integrated shutdown and database-reconnect faults; custom extension handlers can ignore cancellation. |
| `pgbase/job_services.go`, `jobs_options.go`, `backup_schedule.go`; `internal/jobs/service.go`, `management.go` and associated integration tests | Registration/default persistence, stable occurrence identity, claim lease, management and delayed completion. | Final integrated race and multi-process gate. |
| `internal/database/pool.go`, `tx.go`, `retry.go` | Pool ownership, transport policy, borrowed transaction target/isolation checks, rollback deadline and forced connection close, retry classifications/backoff. | Network black-hole and pool-exhaustion qualification; transport security is also reviewed by Security. |
| `internal/schema/cache.go`, `listener.go`, `catalog_lock.go` | Committed immutable catalog publication, monotonic cache generation, listener reconnection/heartbeat/readiness, shared DDL coordination gate. | Full schema compiler/planner/apply inventory belongs to a wider audit; no claim of complete package review. |
| `internal/recordops/service.go`, `transaction.go`, `hooks.go`, `batch.go`, `truncate.go`, `transaction_validation.go`, `idempotency.go`, `cleaner.go`, `delete.go`, `relation_telemetry.go` | Owned and borrowed transaction boundaries; savepoints, callback outcomes, validator timing, ordered relation locks, hydration limits, bounded idempotency cleanup. Six panic cases reproduced and fixed; broader focused suite and independent review passed. | Every field codec, mutation modifier, and relation compiler path has not been re-audited. |
| `internal/recordops/artifact.go`; migrations 4, 14 and 68; `internal/migrate/runner.go`, queue upgrade tests; system embed/checksum tests | Audit → outbox → queue atomicity, upgrade/backfill locks, older SQL writer compatibility and durable delivery identity. Historical-size plans reproduced the growing scan; migration and queue race gates passed. | Multi-million-row migration duration, write amplification and operational upgrade resources remain unmeasured. |
| `internal/realtime/service.go`, `outbox.go`, `status.go` and service/retry/queue integration tests | Current-policy replay, late commit, publication lock, leases, dead letters, retry and retention floor. Prefix retention and indexed pending queue are fixed, repeatedly tested and independently reviewed. | Full HTTP session broker, large fan-out, sustained dead backlog and global publication-lock capacity remain. |
| `internal/lifecycle/registry.go`, registry tests; `pgbase/lifecycle_hooks.go`, `model_lifecycle.go` | Ordered hook snapshots, observer isolation, detached observation budget. Panic outcome regression reproduced, fixed and independently reviewed. | Final integrated extension contracts and custom embedder hooks. |
| `pgbase/trusted_database.go`, `extension_database.go`, `extension_app_transaction.go`, relevant integration tests | Trusted SQL opt-in, caller-owned transactions, nested savepoints and record-outcome handoff. Failed nested rollback now invalidates its connection; focused race and independent execution passed. QA closed the separate whole-invocation rollback deadline gap in `5fc067c9`. | Backend's review of the QA patch is source-only; its actual transport regression is owned by QA. Arbitrary trusted SQL remains explicitly trusted. |
| `pgbase/extension_file_handles.go` close path; `extension_objects.go` transaction state through cleanup | Handle lock order, active-operation drain, storage lease cleanup and shared bounded cleanup context. | Storage path/security semantics are owned by Security; no full object-store audit is claimed here. |
| `internal/runtimesettings/settings.go` | Revision checks, encrypted persisted configuration, serialized notification callbacks, clone boundaries. | Multi-instance key rotation and recovery qualification. |
| `internal/observability/request_log.go` queue/flush/write section | Bounded FIFO queue, flush watermarks, dropped-event accounting and write budget. | The remaining observability package, metrics cardinality, and full logging surface have not been independently covered here. |
| `internal/api/realtime_sessions.go` admission/subscription section | Subscription ownership and request lifecycle traced through the first 260 lines. | Remaining broker/fan-out/cancellation code needs review; do not infer coverage of the entire API package. |
| `internal/schema/physical_manifest.go`, `physical_manifest_builder.go` construction prefix and namespace checks; `internal/doctor/checks.go` history gate and `checks_integration_test.go`; `pgbase/app_integration_test.go` | Migration 68 is included in embedded system history; historical checksums remain immutable. The physical manifest scopes application/relation objects. Ordinary startup and doctor require the latest clean schema; explicit schema-67 rejection cases preserve its history, including `AutoMigrate`. | Full physical inventory/compiler review is not claimed; final PostgreSQL 16/17/18 integration gates belong to the lead. |

Backend review does not replace the Security, Frontend/SDK, performance, or QA
agents' reports. Authentication, archive/storage, dependency/image scans,
browser flows, SDK contracts, CLI/release artifacts, and whole-source CodeQL are
not qualified by this ledger.

## Reciprocal security patch review

Source review of Security commits `fc5f347e`, `29272aff`, `a002599c`, and
`4730e549` found no blocker in the changed paths. The archive check rejects the
first expanded trailing byte, and the complete expanded-stream budget includes
PAX metadata, tar padding and terminators. Valid termination still checks the
gzip checksum; `Pack` and `Unpack` apply the corresponding bound. Small-budget
regressions exercise the boundary without allocating eight GiB.

OIDC verification retains the configured key source, fixed RS256, issuer,
audience and nonce checks. Mandatory issuance/expiry times, optional not-before,
explicit null/invalid values, fractional numeric dates, signing-key purpose,
algorithm/key operations, RSA modulus and exponent were reviewed. The final
follow-ups include the edge cases raised during reciprocal review.

The Backend agent independently executed the archive/OIDC selections on the
Security runtime through `4730e549`:

```sh
go test -race ./internal/backup ./internal/userauth \
  -run '^(TestArchive|TestPack|TestUnpack|TestOIDC)' -count=3 -v
```

Both packages passed (2.019 and 2.083 seconds), with 42 top-level / 210 total
pass events and zero skips. The complete log is in the evidence archive. This
is independent execution and source review, not a second whole-source security
scan.

## Independent review of QA's invocation cleanup fix

The remaining whole-JavaScript-invocation rollback used an uncanceled context
without a deadline. The Backend agent identified that gap and the QA agent
implemented `5fc067c98ce98254aed464c76378e9da6bde4e57`, reusing
`database.BoundedCleanupContext` for the transaction rollback.

Backend read the immutable runtime diff, caller state cleanup, and full
transport fault regression independently and found no blocker. The fixture
wraps an actual PostgreSQL socket, withholds responses only after an
uncommitted write, and retains socket deadline behavior. It verifies a bounded
rollback, discarded connection, one failed outcome, cleared host state, and
zero committed rows. QA observed the old path block beyond three seconds and
the fixed path finish at the shared one-second cleanup deadline; its four
selected extension tests passed race ×3. These execution results are QA's,
not claimed as a duplicate Backend run. See the separate final QA evidence.

## Environment and evidence policy

All destructive or fault-injection tests use disposable databases on an
isolated local PostgreSQL 17.9 cluster, port 56533. The backend agent does not use
the performance agent's PostgreSQL instance. Builds and database tests paused
during the lead's requested measurement windows. The outbox A/B sequence used
precompiled Go 1.26.11 binaries, `GOMAXPROCS=2`, PostgreSQL `shared_buffers=128MB`
and `max_connections=100`. The machine reported nine CPUs, but cgroup CPU and
memory limit files were unavailable; hard resource limits were not verified.
The complete non-comment PostgreSQL configuration and server startup record
are preserved. The host was shared and concurrent activity outside this
agent was not comprehensively recorded.

[The evidence index](evidence/final-backend/README.md) links the raw archive,
SHA-256 manifest, readable A/B statistics, extracted plan summary and environment
provenance. All failed reproductions, the killed process, and both intermediate
test-fixture failures are retained alongside passing results. Some regression
runs occurred in the implementation worktree before their logical commits;
the manifest states those source relationships rather than inventing pre-run
tree hashes. The later Security execution was pinned to an immutable detached
commit, and both A/B binaries have captured source patches and digests.

These are bounded backend fixes and evidence, not a claim that every module or
operational failure mode is qualified. The latest Backend runtime is
`58c1038c`, followed by test-only `68f00863`; this report/evidence change is
documentation-only. QA's later runtime correction must be included in the
lead's integrated commit. Final Go/race, PostgreSQL 16/17/18, SDK/UI/E2E,
security tools, release artifacts, and the release readiness decision require
that integrated source; earlier green results are not substituted for it.
