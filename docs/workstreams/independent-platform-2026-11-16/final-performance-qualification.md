# Final integrated performance qualification — PASS

## Decision and immutable source

**Performance = Pass.** The single authorized final hosted gate completed on
2026-11-16. All **120,000 scheduled arrivals** completed, with **zero dropped,
unscheduled, unexpected-error or incomplete requests**, and scheduled-arrival to
response-validation **p99 = 22.221162 ms**, below the unchanged 250 ms limit.
The functional/overall decision is recorded separately in
[final-integrated-qualification.md](final-integrated-qualification.md).

| Identity | Exact value |
| --- | --- |
| PR | [#148](https://github.com/yakisoba0728/PgBase/pull/148), retained Draft/unmerged |
| Frozen PR runtime/source | `4091c3be20ec8db177886a2a85550924fce37ff5` |
| Actual CI checkout / synthetic merge | `ba838872d72993abf1953dcafc64ba043f978c06` |
| Common PR and checkout Git tree | `653a46758f9e6af9662b77ecfe2f069a2ac94cb4` |
| Workflow | [43861850923](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923) |
| Performance job | [133280386571](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280386571), completed/success |
| Published listener change | [`277c18cbb5333ca2a925ccb84628702c29b7fcd6`](https://github.com/yakisoba0728/PgBase/commit/277c18cbb5333ca2a925ccb84628702c29b7fcd6) |
| Author's local listener commit | `b2c6b223ef65232247baecd00763845baf43efa0`; coordinator mapping preserves the integrated/publication identities |

The artifact's `mixed-environment.txt` first line independently matches the actual
checkout above. Coordinator publication records verify the common tree. This
report and the raw-evidence preservation are subsequent documentation/evidence
changes; they do not represent another runtime change or another load execution.

## Final closeout: fresh independent evidence audit

The separate closeout reviewer downloaded artifact 14224442513 again from GitHub
and matched its original ZIP bytes and SHA-256 against the live API digest and
preserved archive. **All 41 offline checks passed**, without running a benchmark
or database fixture. All 120,000 IDs, operation/owner/status contracts, scheduling,
completion and latency arithmetic were checked; all five recorded p50/p95/p99
series matched independent nearest-rank recomputation.

[Fresh review](evidence/final-integrated/closeout-20261116/performance/independent-performance-review.md),
[result JSON](evidence/final-integrated/closeout-20261116/performance/performance-independent-verification.json),
[offline verifier](evidence/final-integrated/closeout-20261116/performance/verify_existing_performance.py)
and its SHA-256 manifests retain this new audit separately from the previous
execution session's author/reviewer work. The raw CSV SHA-256 is
`2bbcfdef50e9c35e68c9083fb75d7893b1db006e12724b9e2233f7219b3bddb7`.

Physical database integrity is verified through the exact source's successful,
unconditional post-drain SQL assertions and owner traversals, reconciled with
the test log. No standalone post-run database dump exists and no live database
was queried in this closeout. This distinction does not replace the physical SQL
assertions with HTTP status accounting.

## Unchanged gate and actual outcome

| Condition or measurement | Actual result |
| --- | --- |
| Scheduled input | **200 RPS × 600 seconds = 120,000 arrivals**, exactly 5 ms apart |
| Intended / enqueued / attempted / completed | **120,000 / 120,000 / 120,000 / 120,000** |
| Queue-full drops / unscheduled arrivals | **0 / 0** |
| Total recorded errors / unexpected errors | **0 / 0**; total includes drop errors when present |
| Incomplete scheduled / incomplete enqueued work | **0 / 0** |
| Scheduled-to-validated p50 / p95 / p99 | **7.400337 / 12.632966 / 22.221162 ms** |
| Maximum scheduled-to-validated latency | **126.553471 ms** |
| Measured arrival/drain interval | **600.005995104 seconds**; achieved rate reported as **200.00 RPS** |
| Whole Go qualification duration | **749.45 seconds**, including fixture construction and reconciliation |
| Request/background pool maximum | **4**, unchanged in every database observation |
| Workers / admission capacity | **32 / 256**, unchanged |
| PostgreSQL durability | `fsync=on`, `synchronous_commit=on`, `full_page_writes=on` in both before/after snapshots |
| Outcome | **PASS**, original Go assertions and independent CSV recomputation agree |

All 120,000 CSV rows were parsed. Indices are complete, ordered and unique; the
scheduled offset, workload kind and owner match the immutable workload. Every
recorded completed HTTP status matches its operation. The parser separately
counts admission drops, unscheduled work, other errors and incomplete work.
Attempted-request latency includes timeout attempts if they occur; dropped work
never receives a fabricated latency sample. No failing row was filtered away.

The prerequisite closed-loop mixed HTTP test also passed its baseline,
concurrent, saturated and recovery subtests, each with 1,000 requests at
1/8/32/4 workers and zero unexpected errors. Its figures are separate from the
mandatory sustained open-loop gate and are not used to substitute for it.

### Separately ranked timing components

| Metric | p50 ms | p95 ms | p99 ms | Maximum ms |
| --- | ---: | ---: | ---: | ---: |
| Scheduled arrival → validation complete | 7.400337 | 12.632966 | 22.221162 | 126.553471 |
| HTTP service measurement | 6.735393 | 11.790135 | 21.098702 | 126.416111 |
| Generator plus worker queue | 0.188780 | 0.920248 | 1.146164 | 11.735461 |
| Generator lateness | 0.183692 | 0.915344 | 1.137495 | 11.563844 |
| Worker queue wait | 0.004701 | 0.006699 | 0.016778 | 1.836818 |

Each distribution has 120,000 observations. Percentiles use the same nearest-rank
rule as the Go test and are **not additive**. Every ten-second bucket had zero
errors/drops; the largest bucket p99 was 72.468597 ms in scheduled seconds
390–399. Bucket statistics are additional evidence, not replacement thresholds.

## Data integrity after draining

The unchanged successful Go test executes post-load SQL counts, cursor traversal,
unique identity checks, owner isolation, relation validation and ETag/CRUD
assertions. A test log containing PASS means these assertions actually completed;
the log does not print every successful SQL count individually.

The passed SQL assertions require **24,256 durable `open_notes` rows**, including
**12,000 created** and **12,000 updated** titles. The two owner traversals each
require **12,128 unique rows**, **6,000 successful creates** and **6,000 successful
updates**, with no remaining cursor. Each owner also performed 6,000 successful
deletes. Independent response arithmetic starts with 12,128 seeded rows per
owner and reconciles `12,128 + 6,000 creates − 6,000 deletes = 12,128` final rows.
That arithmetic supplements the executed SQL/identity checks; HTTP status alone
is not treated as the durability oracle. Foreign-owner fetches must remain 404.

## Minimal listener change and full connection budget

The long-lived consumer is the **schema catalog listener**, not the Realtime
outbox worker. Previously it intentionally held one of four pool slots, leaving
three available for requests/background work. Historical deployment documentation
explicitly described that reservation. It was not newly discovered as a leak,
and it was not established as the unique cause of the historical request loss.

The latest task explicitly permits an additional dedicated listener while
retaining four request-pool slots. The bounded correction acquires the normally
initialized connection and transfers ownership with `pgxpool.Conn.Hijack`; the
listener closes that connection using its existing bounded cleanup context.
TLS validation, safe search path, UTC, statement timeout, application identity
and the shared backup-writer gate remain established by the original connection
initialization. Reconnect briefly needs a pool acquisition before detaching.

| Client session class in this load fixture | Count |
| --- | ---: |
| Configured request/background pool maximum | 4 |
| Dedicated schema listener, newly separated | 1 |
| Existing application backup writer-gate session | 1 |
| Existing independent diagnostic observer | 1 |
| **Total persistent client sessions** | **7**, versus 6 under the prior listener budget |

The diagnostic observer is present only during diagnostic collection; production is not assumed to keep that observer running. This budget counts persistent client sessions, not a ceiling on PostgreSQL maintenance workers or operator connections.

The sampler excludes its observer connection. Six stable application PIDs appear
in all 6,000 samples. It reports six database-activity backends in 5,850 samples
and seven in 150 samples. Every extra row has an `autovacuum:` VACUUM/ANALYZE query
prefix, identifying a background maintenance worker; `backend_type` itself was
not captured. Thus the sampled peak is **eight database-activity backends when
the observer is included**, comprising seven client sessions plus maintenance.
It is not an eight-connection request pool. Other production coordination and
operator sessions still require their own capacity budget.

### Focused author and independent safety checks

The author first ran the new capacity/lifecycle regression against the unchanged
listener: the fourth simultaneous request acquisition timed out, with pool
maximum four, one reserved slot and three available requests, both initially and
after reconnect. With the ownership transfer, the same regression passed with
four held request connections and one additional listener backend. It also
verified the listener's shared backup lock, termination/reconnect, cancellation,
old/new backend disappearance and exclusive backup-gate acquisition after pool
shutdown.

Author affected-package race checks passed **30 top-level tests + 7 subtests**
with zero failures/skips; integration/load vet passed. Agent C independently
restored the original listener, reproduced the fourth-slot failure, restored the
integrated candidate and passed its separately selected **25 top-level + 17
subtests** under PG17.9 race. These counts describe different selected executions;
they are not added into a count of distinct tests. Initial wrapper executable
permission failure is retained separately from the actual RED reproduction.

The only load-diagnostic change adds `full_page_writes` to a read-only settings
query so the final artifact directly captures that durability property. No
workload, threshold, scheduling, worker, queue or PostgreSQL setting changed.

## Test host and diagnostic observations

The isolated GitHub runner used Ubuntu 24.04, Go 1.26.11 linux/amd64,
`GOMAXPROCS=4`, four virtual CPUs reporting Intel Xeon Platinum 8573C under a
Microsoft hypervisor, and 16,759,304,192 bytes of reported host RAM. PostgreSQL
17.9 ran in the pinned disposable Bookworm image
`sha256:47f917f7409eacd22fc5dfb1dee634e1b55cf0c01d1a7eb701be2227a03e0641`.
Docker reported no explicit memory/CPU quota and 64 MiB shared memory.
`max_connections=100`, shared buffers 128 MiB, work memory 4 MiB,
`wal_sync_method=fdatasync`, checkpoint timeout 300 seconds and checkpoint
completion target 0.9 were recorded. IO/WAL timing was enabled for observation;
durability was retained. The application server, HTTP client and generator share
the Go process; its allocations/GC/CPU counters are not server-only figures.

| Observation | Recorded value and interpretation |
| --- | --- |
| Database samples | 6,000 at nominal 100 ms; no diagnostic error |
| All four pool slots acquired | 93/6,000 samples (1.55%); sampled occupancy, not exact utilization duration |
| Pool acquisition waits / aggregate wait / cancellations | 4,978 / 30.171161454 seconds / 0; aggregate wait overlaps across callers |
| Cluster WAL | 432,024,542 additional bytes; 183,820 syncs and 32.825648 seconds cumulative sync time |
| Active sampled WAL waits | 1,306 `IO/WalSync` and 57 `LWLock/WALWrite` observations; not per-request wait duration |
| Aligned whole-host vmstat | 600 one-second samples, mean idle 58.0833%, mean IO wait 1.0867%; no swap use/in/out |
| Combined Go process | 450.657910 busy CPU seconds; 64.598140 GC CPU seconds; 1,303 GC cycles; 131.963042 ms total pause |

The producer, queue, service, pool, database and host observations are retained
separately. This run does not show the previous multi-second queue-loss burst.
Sampled query ages are not exact wait durations; cluster WAL/IO include other
work; host averages do not rule out transient scheduling interference. Continuous
queue occupancy and rejected-arrival generator timestamps are absent from the
unchanged harness. Those limitations remain explicit even though this run has
no rejected arrivals. The Docker streaming file contains terminal control bytes
and is retained raw, not presented as a timestamp-aligned series.

## Historical failure, causal limits and stop boundary

Historical [run 43820007603](https://github.com/yakisoba0728/PgBase/actions/runs/43820007603)
remains a **failure**: 119,453/120,000 completed, 547 queue-full drops, and overall
p99 209.489023 ms. Its passing latency percentile never excused loss. Its source,
host invocation and pool ownership differ from this candidate; its raw ZIP hash
is `d2411a4837ffd38a5fd3c2b67423240f07de4eed9ed0f5d4038af59ca628cf6e`.

The registered three hypotheses were listener slot reservation, WAL/COMMIT stalls
and generator/scheduler delay. The focused test proves the changed connection
availability. The final gate proves this one candidate/fixture met its unchanged
contract. Neither result establishes that the listener uniquely caused all 547
old losses, that separating it alone explains the latency difference, or that
shared-host/WAL variability has been permanently removed. No matched-host causal
A/B estimate is claimed. One disposable CI host is not a guarantee for unlimited
production traffic, other schemas, network topology or storage devices.

Exactly **one final 600-second gate** ran in this round. No second attempt,
identical-code retry, ABBA campaign, durability weakening or threshold adjustment
was used. The current performance gate is complete; no automatic investigation
or additional load test remains running. A later regression should be reproduced
on an explicitly recorded disposable environment before selecting a new change.

## Raw evidence, independent recomputation and reproduction

Original [artifact 14224442513](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224442513)
is **3,528,654 bytes**, SHA-256
`82f0397e6b0ab5e1d71c96ebc2c4af35137fc8638d60c3c262b8208d5935d84e`.
The downloaded original ZIP's byte count and digest match GitHub metadata.
GitHub's original Actions expiry is 2026-11-30 12:08:39 UTC; an identical ZIP is
also committed here to preserve the raw evidence beyond Actions retention:

- [Original raw ZIP](evidence/final-integrated/performance/final-gate/pgbase-final-mixed-http.zip): full 120,000-row CSV, database snapshots, host/DB observers, environment and both Go logs.
- [Artifact metadata](evidence/final-integrated/performance/final-gate/artifact-metadata.json), [terminal job metadata](evidence/final-integrated/performance/final-gate/job.json) and [SHA-256 manifest](evidence/final-integrated/performance/final-gate/SHA256SUMS).
- [Derived analysis](evidence/final-integrated/performance/final-gate/final-performance-analysis.json) and [analysis program](evidence/final-integrated/performance/final-gate/analyze_final_performance.py), with all 60 ten-second buckets, errors, settings, source identity and decompressed-file digests.
- [Author RED/GREEN and race evidence](evidence/final-integrated/performance/), including its preregistered protocol and initial fixture failure.

Agent C received the raw files and metadata before this author's final numerical
summary and used a separately written parser. It independently reproduced the
exact counts, nearest-rank p50/p95/p99, schedule/workload/owner contracts, pool and
durability checks, passed post-load assertions, checkout identity, ZIP bytes/hash
and the extra autovacuum observations. This is separate from the author's own
analysis and does not describe author self-review as independent QA.

To recompute the report without running another load test, extract the committed
ZIP into `raw-final` and invoke:

```sh
python analyze_final_performance.py raw-final analysis.json \
  --run-id 43861850923 --job-id 133280386571 \
  --source-sha 4091c3be20ec8db177886a2a85550924fce37ff5 \
  --checkout-sha ba838872d72993abf1953dcafc64ba043f978c06 \
  --tree 653a46758f9e6af9662b77ecfe2f069a2ac94cb4
```

A future manual runtime reproduction uses the existing
`TestOpenLoopHTTPQualification`, Go 1.26.11, disposable PG17.9,
`PGBASE_OPEN_LOOP_RPS=200`, `PGBASE_OPEN_LOOP_SECONDS=600`,
`PGBASE_OPEN_LOOP_DIAGNOSTICS=1`, an absolute `PGBASE_LOAD_EVIDENCE_DIR`,
`GOMAXPROCS=4` and the unchanged workflow observer. It requires all original
correctness/zero-loss/latency checks and enabled durability; this report does not
authorize or launch that future execution.
