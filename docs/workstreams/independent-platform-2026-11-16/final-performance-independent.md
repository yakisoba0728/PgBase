# Independent performance review

Reviewer: `/root/performance_review`, a separately spawned agent that did not implement the allocation changes, the root load harness, or the original analysis script. This report records independent source and evidence inspection; it does not turn parallel CI jobs into independent AI review.

## Scope and source identity

Initial review checkout: `ffe78b117f958fe779e35ef5ff957ee83cda6669`. Additional source-only review covered native harness commits `da395ff87c73fe24421b2bc2370cc10c55827191` and `7af642770cca2f04b3f64a5f89ac4afbc97a132d`. Later integrated runtime commits require their own qualification.

Reviewed files and call boundaries:

| Area | Inspected source/evidence | Boundary checked |
| --- | --- | --- |
| Reconstruction | Both historical worktrees, recorded source patches, harness hashes, original commits | Baseline and candidate differ by the stated cursor/allocation changes; harness bytes match |
| Analysis | `scripts/analyze_repeated_load.py`, raw logs, ledger, environment | Complete observations, sample denominators, pair direction, quantile units, independent replication unit, ratios and uncertainty |
| Native reads | `internal/query/load_benchmark_integration_test.go`, `internal/query/codec.go`, `internal/query/root_page.go`, `internal/testutil/membership.go` | Fixture construction, warmup, timers, allocation accounting, native membership predicate and actual execution plans |
| Historical HTTP | `pgbase/mixed_load_integration_test.go` at the registered common harness | Four stages, fixed pool, owner isolation, mutations, relation/cursor checks and closed-loop measurement |
| Final open-loop | `pgbase/open_loop_load_integration_test.go` | Fixed scheduled arrivals, admission failures, cancellation, latency origin, response validation and durable state traversal |
| Final index experiment | Commits `da395ff8` and `7af64277` | Untimed semantic reference; index maintenance probe after timed reads; rollback errors remain visible |
| Profiling | All 24 original CPU profiles, with their matching four test binaries | Rebuilt pooled summaries; CPU scope and setup contamination; no inference that allocation bytes equal RSS |

This is a performance-methodology and selected hot-path review. It does not certify every backend package, every SQL shape, full security coverage, production hardware, cold caches, large relation fan-out or multi-day reliability.

## Independent reconstruction checks

The raw source is retained under [historical-local](evidence/final-performance/historical-local/). The reviewer independently compared file bytes with the referenced Git objects:

- Baseline A is `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b`.
- Candidate B has the runtime bytes from cursor bound `312f1c614b4c0d08c32a922dfb4fb2040ebaa3b6` and allocation change `c0a37c385b5a49bbf6fd9cef624eace9b941482f`.
- Both HTTP harnesses exactly match `7c2e82234e188d623181048692d78975438d81f6`; SHA-256 is `9d9b3ebfc11c37c3d6629ac2f74c007c08db22762b870899cf1db666f5d7174a`.
- Both native harnesses exactly match `e2f78171e2233a32ab4420a107cb5715485a24a5` with the single common warmup change from five to 100; SHA-256 is `db4fab087667cd326fa761f2d04b3008bff23746b4e434459b347aedbcdba9bb`.
- The native fixture has 500,000 rows, 20 fields, page size 50, GOMAXPROCS=4 and the same seed algorithm. Each invocation creates a fresh database with newly generated IDs. The fixtures share distribution and construction, not exact physical bytes or storage/cache history.
- Go 1.26.11 and PostgreSQL 17.9 are recorded. PostgreSQL has fsync and synchronous_commit enabled, max_connections=100, shared_buffers=16,384 blocks, work_mem=4,096 KiB, track_io_timing enabled and max_parallel_workers_per_gather=2. The host reports nine CPUs and approximately 9.7 GiB memory; exclusive hardware isolation is not established.

All 24 invocations exited successfully. Each of twelve native invocations contains exactly twelve 1,000-operation cases; each of twelve HTTP invocations contains four 1,000-request stages. Independently checked totals are **144,000 measured native operations and 48,000 measured HTTP requests**, with zero reported request errors and zero canceled pool acquisitions. The first native process took 149.797 seconds, much longer than later invocations; it remains in the ledger and analysis.

The independent parser checks every one of the 160 workload/metric observation rows. Their before/after values and geometric ratios agree with the author's report. Re-running the current author script changes only insignificant floating-point representation in HTTP pool-wait conversions because multiplication/division grouping changed; classifications and substantive numbers agree. The exact comparison failure from a byte-for-byte JSON check was investigated, not suppressed.

## Statistical findings that may be claimed

The registered replication unit is a complete invocation, paired as `(A1,B2)`, `(A4,B3)`, `(A5,B6)`, `(A8,B7)`, `(A9,B10)`, `(A12,B11)`. Individual requests within one invocation are not treated as independent replications. The reported statistic is the geometric mean of six B/A ratios; it is not the ratio of the two medians.

The second implementation enumerated all **46,656** possible six-pair bootstrap resamples for selected endpoints, rather than reusing the author's 20,000 random resamples. Its intervals are consistent with the registered calculation. Small Monte Carlo differences do not change the conclusions. The registered intervals remain the primary report; exhaustive values provide an independent arithmetic check.

| Endpoint | A median | B median | Geometric B/A | Registered 95% interval | Supported interpretation |
| --- | ---: | ---: | ---: | --- | --- |
| Native list_50/c1 allocated bytes | 1,116,872.5 B | 517,949.5 B | 0.46375 | 0.46370–0.46380 | Approximately 53.6% fewer allocated bytes per operation |
| Native list_50/c1 p99 | 15.460 ms | 7.986 ms | 0.60033 | 0.34344–0.91302 | Improvement in this repeated experiment; the previous severe worsening did not reproduce |
| Native list_50/c1 throughput | 281.70/s | 388.75/s | 1.40134 | 1.17803–1.76455 | Improvement in this repeated experiment |
| HTTP/c32 allocated bytes | 755,918.15 B | 665,119.00 B | 0.88925 | 0.87464–0.90801 | Approximately 11.1% fewer allocated bytes per request |
| HTTP/c32 p99 | 209.257 ms | 306.531 ms | 1.38438 | 0.79451–2.41803 | **Unresolved adverse estimate; no equivalence or absence-of-regression conclusion** |
| HTTP/c32 throughput | 449.00/s | 458.79/s | 0.97119 | 0.83539–1.11891 | Inconclusive |
| HTTP/c8 throughput, secondary | 490.70/s | 451.365/s | 0.94067 | 0.88960–0.98477 | A small adverse interval remains; approximately 5.9% paired throughput loss |
| HTTP/c8 pool wait per request, secondary | 10.557 ms | 11.680 ms | 1.06698 | 1.00855–1.14213 | Pool waiting also increased in this secondary comparison |

The exhaustive HTTP/c32 p99 interval is 0.79451–2.44979, also inconclusive. All three ABBA blocks have a higher geometric-average HTTP/c32 p99 in B. That directional consistency is a reason to continue investigating; only three blocks do not support a new post hoc confidence claim. The secondary HTTP/c8 results are exploratory because many endpoints were examined, but they must remain visible.

The author's `bounded_10pct_equivalence` classification checks whether the interval lies within 0.90–1.10. A small directional difference can coexist with that bounded statement; the numerical interval must remain visible. A green workflow that successfully runs the experiment is not proof of no performance regression.

Six pairs, 1,000 requests per HTTP stage and a shared host are inadequate for a universal p99 or capacity claim. In particular, each per-run p99 is near the tenth-highest request in a short stage. No request-level latency series was preserved for these historical closed-loop runs, so intra-run tail uncertainty cannot be independently reconstructed from the logs. Later larger HTTP experiments must be reported separately by host and source.

## Independent SQL finding: a statistics-dependent plan change

The historical filtered plans are **not identical across all twelve invocations**.

Eleven plans use the ordered root index, reject 4,991 roots and return 51 roots after examining 5,042. Their membership lookup executes 51 times. The explicit EXPLAIN execution times range from 0.807 to 1.688 ms. This confirms an opportunity for an index that combines the filter with the default ordering; it does not demonstrate that such an index should be added to every application table.

The `04-before-query.log` plan is materially different:

- Gather Merge and per-worker top-N sort follow a parallel sequential scan of `pgbase_meta.record_creation_ordinals`.
- The sidecar produces approximately 166,667 rows per worker across three loops, then drives **500,000** primary-key probes into `benchmark_records`.
- The plan reports 1,981,269 shared buffer hits, 22,930 shared reads and 82.118 ms aggregate shared I/O read time.
- Execution is **470.356 ms** for the same 51 output rows.
- The sidecar estimate is only 4,166 rows per worker versus 166,667 actual. This is evidence of a large estimation error.

The synthetic helper bulk-populates the sidecar, but the historical harness explicitly ANALYZEs only the application table. Missing explicit sidecar statistics and asynchronous auto-analyze are a plausible fixture-level cause of the plan change. The old run did not record a contemporaneous `pg_stats` snapshot, so that cause remains an inference rather than a measured fact. PostgreSQL logs also record two approximately one-second ANALYZE waits on autovacuum during setup.

This 470.356 ms result belongs to the **untimed EXPLAIN call before the workload warmups**. It is not a captured plan for a timed tail request. The same invocation's timed filtered/c1 case completes 1,000 operations in approximately 3.424 seconds, with p50=2.991 ms and p99=22.63 ms. The expensive plan therefore did not persist for every timed filtered operation. No claim is made that it caused the measured native p99 or the separate HTTP regression.

The finding was sent to the root agent before the final scale experiment. The requested correction is to ANALYZE the sidecar after synthetic bulk loading, consistently in both final index arms, and retain the historical outlier unchanged. Any new A/B run with this corrected harness is a **separate experiment**, not a replacement or deletion of the original results. Extracted plan nodes are preserved in [filtered-plans.json](evidence/performance-independent/filtered-plans.json).

## CPU, allocation, pool and scheduler interpretation

The reviewer rebuilt pooled CPU summaries from the original profiles and their matching test binaries. Native sampled CPU totals are 223.31 seconds in A and 144.57 seconds in B; `EncodeRecord` cumulative sampled CPU is 57.93 and 39.99 seconds. Runtime object scanning, clearing and JSON sorting are also lower. The inspected allocation change avoids repeated field-slice copies/sorts, preallocates scan destinations and reuses already-measured scalar wire sizes. These source mechanisms and profiles support the allocation result.

Those CPU totals include fixture construction, warmups, benchmark calibration and cleanup. They are not isolated timed-query CPU costs, and the profile wall durations differ. The HTTP whole-invocation profile includes Argon2 setup, client transport, gzip and all four stages; it does not isolate the c32 tail. Its sampled CPU totals are 106.06 seconds in A and 99.40 seconds in B. Neither profile proves why a particular request was slow.

HTTP memory accounting covers client and embedded server. Maximum resident set reported by process rusage includes setup, including password hashing. Allocated bytes, live heap and maximum RSS are different measurements; a 53.6% allocation reduction is not a 53.6% RSS reduction.

Pool waiting is reported separately and is substantial at HTTP/c32 with a pool of four connections. It is aggregate wait across concurrent acquisitions, so it can exceed elapsed wall time. Context-switch counts and whole-process CPU are retained, but no scheduler trace or per-request SQL wait attribution was captured. `pg_stat_statements` was unavailable in the local PostgreSQL installation. The HTTP tail cause is therefore not fully isolated by these artifacts.

## Review of new qualification harnesses

The fixed-arrival harness starts latency at the intended arrival time, includes generator/queue delay, and continues scheduling independently of responses. Its bounded queue records drops; scheduling cancellation records unscheduled arrivals; attempted requests that time out before headers remain in the latency series. All incomplete/error cases fail the test, even when no latency sample exists for a dropped request. Byte/allocation rates use intended requests, which is appropriate for a passing zero-drop run; a failing overloaded run must not be presented as a normal per-completed-request allocation result.

The harness validates owner, record identity, update ETag progression, field values, relation expansion, filtering and page order. After load it checks physical counts, traverses all owner-visible pages, counts created/updated records, checks uniqueness and verifies cross-owner denial. Distinct update/delete targets avoid artificial request dependencies; this is not a hot-row contention workload. Its runtime CPU counters include client and server in one process and are runtime accounting rather than separate database or operating-system CPU measurements.

For final native/index runs, the original benchmark only checked page length. Independent review requested stronger untimed semantics. Commits `da395ff8` and `7af64277` add an independent SQL reference for the first 51 filtered identities, verify the returned first 50 IDs in order and their priority=42, and require continuation. The index write probe runs after all timed reads, uses EXPLAIN ANALYZE/BUFFERS/WAL on a 1,000-row synthetic update, and rolls back through the guarded transaction helper. Exact sentinel comparison preserves visibility of a joined rollback failure. This measures SQL index-maintenance cost, not the full Record Operations write API.

The index build and reference query warm caches; the experiment is explicitly warm. The diagnostic index flag is confined to a disposable benchmark fixture, not a migration or an automatic production tuning change. No source blocker was found in these reviewed harnesses. Their final execution results must be attached separately.

## Current independent disposition

**The historical allocation improvement is supported; an across-the-board latency improvement is not.** Native single-concurrency improvement is reproducible within the registered first experiment, while HTTP/c32 tail latency remains unresolved and HTTP/c8 shows an adverse secondary throughput interval. The SQL review found a real fixture statistics confound that must be controlled in additional native measurements.

This report alone cannot support `Ready` for large commercial deployments. Pending final runtime, larger HTTP and scale/fault results must be judged against the registered gates. No unlimited concurrency, multi-region ordering, long-term soak or production recovery capacity is claimed here.

## Reproduction and independent artifacts

Run from the repository root:

```bash
python3 docs/workstreams/independent-platform-2026-11-16/evidence/performance-independent/recompute.py \
  docs/workstreams/independent-platform-2026-11-16/evidence/final-performance/historical-local \
  /tmp/pgbase-independent-statistics
```

[Independent statistics](evidence/performance-independent/independent-statistics.json), [command output](evidence/performance-independent/recompute.log), [profile commands and input hashes](evidence/performance-independent/profile-commands.json), and the four CPU summaries are retained with a SHA-256 manifest. The recomputation script does not import the author's parser or bootstrap implementation.
