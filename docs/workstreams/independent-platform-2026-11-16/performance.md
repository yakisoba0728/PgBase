# Measured performance and qualification boundaries

The current query change reduces the reproduced 50-row page allocation from **1,116,841 to 517,981 bytes per operation (53.6%)** at concurrency 1. The reduction remains approximately 53% in the deep-cursor and filtered-page cases. Profiling attributes the removed work to repeated field-slice cloning/sorting, growing scan-destination slices, and a second JSON serialization of unchanged scalar rows.

**Latency results are mixed and include regressions.** In the same paired run, the concurrency-1 first-page p99 increased from 5.302ms to 35.950ms, and filtered-page concurrency-32 p99 increased from 49.03ms to 140.0ms. Every mixed HTTP stage also had a worse p99. These data support an allocation improvement; they do not establish a general latency, throughput, scaling or production-capacity improvement.

The complete portable evidence is in [evidence/performance-2026-11-16](evidence/performance-2026-11-16/README.md), including [machine-readable measurements](evidence/performance-2026-11-16/measurements.json), raw outputs, sampled CPU/allocation excerpts, native SQL plans, and a [SHA-256 manifest](evidence/performance-2026-11-16/manifest.json). Raw profiles and test binaries are reproducible from the commands below and are not committed.

## Current source and method

| Item | Source / condition |
| --- | --- |
| Baseline runtime | `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b` |
| Candidate runtime | `e1f5e2eb3ca77174d9f4e0cf655f0be0eb8ac5ca`, including cursor guard `d0322ace2b6a51aa4e6e1ab6ac4a68ff9fc4fcfd` |
| Identical mixed HTTP harness used in both measured trees | `8d0ceddbcb2687600e5b10182d1fa9e357934fa9` |
| Native benchmark instrumentation | `8a3904980e052f0e656b71ce30e6d960fccfe795`; the later committed documentation comment does not affect executable code |
| Stronger HTTP contract assertions, separately validated | `15b19c36443c3031c5ed0d979ada326b05b9f19d` |
| Toolchain / host | Go 1.26.11, linux/amd64, AMD EPYC 9V74; runtime reports 9 CPUs, `GOMAXPROCS=4` |
| PostgreSQL | 17.9, compiled with GCC 14.2.0, C.UTF-8, `--without-icu --without-readline`, `pg_trgm` installed, `max_connections=100` |
| Isolation | Private disposable databases on a local TCP-only cluster; no production server, data, network or credentials |
| Execution order | Native baseline, native candidate, HTTP baseline, HTTP candidate; 2026-11-15 23:46:50–23:48:58 UTC |
| Sampling | One paired run, CPU and memory profiling enabled identically; other project agents paused CPU-heavy work during the pair |

Each native fixture creates 500,000 rows with 20 typed fields and validates the physical row count, creation-ordinal count, and native visible count. It walks 10,000 rows to prepare the deep cursor and analyzes the table. The first-page and `priority = 42` execution plans are also executed before measurement, so these are warm workloads. Each measured subbenchmark performs five warmup operations before 1,000 measured operations at concurrency 1, 8 or 32. Go benchmark calibration and fixture preparation are outside those sample counts but are present in the whole-process profiles. The native fixture uses the existing default request pool; the HTTP fixture fixes its pool at four connections.

The native profile exercises `query.Service` under the fixture administrator principal. It includes the database transaction, native membership boundary, pool waiting and record conversion; it excludes HTTP parsing, user authentication, HTTP response serialization and writes. The separate HTTP experiment covers those application paths with authenticated user owners and relation expansion on a smaller dataset. Neither is an open-loop load model or a coordinated-omission-corrected service-level assessment.

Both revisions run the same schema, synthetic value distributions, query shapes, warmup and iteration counts. Each receives a newly created database; generated fixture identifiers and backend cache state are not byte-for-byte snapshots. The shared host is not a dedicated, CPU-pinned benchmark machine. One pair does not distinguish a stable latency regression from scheduler, storage or profiling variation. No unfavorable result is removed, and no extra runtime change is justified from these latency samples alone.

## Published source reconstruction

The measurement artifacts retain the actual original local source identifiers. Their patches and relevant source bytes were independently matched to the published commits in [runtime-commit-map.json](evidence/runtime-commit-map.json). The combined CI runtimes `7619dbf790c9ccf88079247482b5db18fe8f018a` and `5e4b71d1f602c9c136809bf65f2375bf75fc4a9f` include other security/SDK/dependency changes and are not relabeled as the entire measured performance revision.

To reconstruct the paired comparison, create both worktrees at `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b`; apply published cursor commit `312f1c614b4c0d08c32a922dfb4fb2040ebaa3b6` and query commit `c0a37c385b5a49bbf6fd9cef624eace9b941482f` only to the candidate. Install the same native benchmark file from `e2f78171e2233a32ab4420a107cb5715485a24a5` and original mixed HTTP harness from `7c2e82234e188d623181048692d78975438d81f6` in both trees. Use the environment/commands below. The stronger HTTP assertions in `bf4aa46e1f09300a2995f91c022fa0b67edeede5` have separate evidence and were not used for the paired resource measurements.

The published native benchmark adds one non-executable comment (`// Add PGBASE_BENCH_EXPLAIN=1 and -v to print native SQL execution plans.`). Removing that line reproduces the exact common-harness checksum in the manifest; retaining it has no executable effect. The original locked artifacts and their 25 hashes have not been altered.

From a clone containing the PR branch's published history, the following prepares the two trees without relying on unpublished agent commit objects:

```sh
git fetch origin refactor/independent-platform-20261116
pgbase_bench_dir=$(mktemp -d)
git worktree add --detach "$pgbase_bench_dir/before" 6dd8f6449f7b6bc1a986ff7444b898776abf1c7b
git worktree add --detach "$pgbase_bench_dir/after" 6dd8f6449f7b6bc1a986ff7444b898776abf1c7b
git show 312f1c614b4c0d08c32a922dfb4fb2040ebaa3b6 --format= | git -C "$pgbase_bench_dir/after" apply
git show c0a37c385b5a49bbf6fd9cef624eace9b941482f --format= | git -C "$pgbase_bench_dir/after" apply
for pgbase_side in before after; do
  git show e2f78171e2233a32ab4420a107cb5715485a24a5:internal/query/load_benchmark_integration_test.go \
    > "$pgbase_bench_dir/$pgbase_side/internal/query/load_benchmark_integration_test.go"
  git show 7c2e82234e188d623181048692d78975438d81f6:pgbase/mixed_load_integration_test.go \
    > "$pgbase_bench_dir/$pgbase_side/pgbase/mixed_load_integration_test.go"
done
```

Run the measurement commands below separately from each prepared tree, with the same disposable database server and toolchain. The candidate runtime edits are intentionally uncommitted benchmark inputs; compare the relevant file hashes with the manifest. Use separate profile output directories so the candidate does not overwrite baseline evidence.

## Paired query results

Every row below contains 1,000 samples per revision. All 24,000 queries completed without an error. Latencies are milliseconds; throughput is query-service operations/second.

| Workload | Concurrency | B/op before → after | Allocs/op before → after | p50 before → after | p95 before → after | p99 before → after | Queries/s before → after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| get | 1 | 82,954 → 47,566 (-42.7%) | 445 → 425 | 0.726 → 0.630 | 1.243 → 1.302 | 1.954 → 3.590 | 1,237 → 1,253 |
| get | 8 | 83,393 → 48,172 (-42.2%) | 447 → 428 | 1.519 → 1.087 | 3.123 → 2.226 | 5.629 → 3.675 | 4,387 → 6,048 |
| get | 32 | 83,038 → 47,773 (-42.5%) | 448 → 428 | 5.617 → 4.256 | 8.178 → 6.175 | 9.476 → 7.721 | 5,422 → 7,224 |
| list_50 | 1 | 1,116,841 → 517,981 (-53.6%) | 10,741 → 7,970 | 2.902 → 2.560 | 4.078 → 11.090 | 5.302 → 35.950 | 331.600 → 240.700 |
| list_50 | 8 | 1,116,444 → 517,728 (-53.6%) | 10,740 → 7,969 | 6.941 → 5.971 | 10.730 → 9.879 | 17.420 → 12.030 | 1,091 → 1,281 |
| list_50 | 32 | 1,116,391 → 517,813 (-53.6%) | 10,741 → 7,971 | 30.030 → 20.930 | 36.100 → 28.010 | 39.920 → 56.780 | 1,054 → 1,303 |
| deep_cursor_50 | 1 | 1,125,345 → 526,348 (-53.2%) | 10,862 → 8,090 | 4.192 → 3.332 | 5.838 → 4.607 | 8.667 → 8.157 | 222.900 → 265.700 |
| deep_cursor_50 | 8 | 1,124,611 → 526,062 (-53.2%) | 10,859 → 8,089 | 11.200 → 7.527 | 21.970 → 15.810 | 46.510 → 82.550 | 623.800 → 804.100 |
| deep_cursor_50 | 32 | 1,124,699 → 526,169 (-53.2%) | 10,861 → 8,091 | 37.920 → 27.320 | 47.800 → 34.260 | 51.040 → 36.450 | 825.600 → 1,146 |
| filtered_50 | 1 | 1,123,180 → 522,364 (-53.5%) | 10,838 → 8,067 | 2.988 → 2.426 | 4.529 → 3.889 | 8.708 → 15.870 | 302.300 → 311.500 |
| filtered_50 | 8 | 1,123,183 → 521,938 (-53.5%) | 10,835 → 8,064 | 7.481 → 8.460 | 11.670 → 15.560 | 13.610 → 19.710 | 1,026 → 878.500 |
| filtered_50 | 32 | 1,122,428 → 521,978 (-53.5%) | 10,836 → 8,066 | 31.370 → 34.170 | 39.470 → 89.390 | 49.030 → 140 | 991.800 → 769.800 |

## Paired mixed HTTP results

Each stage has 1,000 requests: 100 create, 100 update, 100 delete, 100 get, 500 first-page reads and 100 cursor reads. Both revisions completed all four stages; all 8,000 requests returned expected statuses and passed the data-contract assertions.

| Workers / stage | Requests/s before → after | p50 before → after | p95 before → after | p99 before → after | Allocated bytes/request before → after |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 / baseline | 224.39 → 229.53 | 3.84951ms → 3.801787ms | 6.180592ms → 5.948286ms | 10.600051ms → 13.19291ms | 840,158.1 → 777,141.7 (-7.5%) |
| 8 / concurrent | 492.37 → 526.41 | 14.098534ms → 12.995814ms | 28.829101ms → 22.633689ms | 58.042706ms → 79.581866ms | 783,330.1 → 732,975.0 (-6.4%) |
| 32 / saturated | 484.53 → 536.71 | 57.376602ms → 49.782849ms | 116.347243ms → 86.915925ms | 162.142167ms → 227.642233ms | 742,198.0 → 682,356.6 (-8.1%) |
| 4 / recovery | 532.41 → 522.04 | 6.820252ms → 6.561469ms | 10.891194ms → 12.450093ms | 13.254455ms → 27.405869ms | 781,845.5 → 711,816.2 (-9.0%) |

### Mixed HTTP resource observations

| Workers | GC cycles before → after | GC pause before → after | Heap before/after stage, baseline bytes | Heap before/after stage, candidate bytes | Pool waits before → after | Pool wait total before → after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 128 → 117 | 19.167559ms → 17.042033ms | 99,466,976 / 9,446,880 | 98,358,840 / 14,253,496 | 0 → 4 | 0s → 1.951632ms |
| 8 | 107 → 103 | 43.434499ms → 27.382717ms | 11,934,992 / 10,364,872 | 11,233,776 / 9,761,120 | 3438 → 3435 | 10.725491739s → 9.919313839s |
| 32 | 88 → 81 | 19.311776ms → 13.317863ms | 12,863,672 / 14,955,792 | 11,822,776 / 15,299,544 | 3351 → 3328 | 57.897923188s → 52.436618991s |
| 4 | 102 → 92 | 14.694209ms → 25.105556ms | 17,510,832 / 13,795,336 | 17,348,720 / 10,375,272 | 2454 → 2622 | 1.85037135s → 2.281133936s |


## Interpretation and measured allocation causes

The 500,000-row allocation profile independently reproduces the previous 1.12MB observation. In the baseline whole-process `alloc_space` profile, `fieldsByOrdinal` accounts for 40.88% of sampled allocated bytes cumulatively: approximately 4.07GiB, with the large slice copy at `codec.go` responsible for most of it. Every row previously cloned the full `[]schema.Field` and invoked reflection-based sorting even though catalog fields were already ordered. The current helper first checks ordinal/name order and returns a documented read-only slice for that case. Unordered caller-owned schemas still receive a copied, sorted slice. No global cache or catalog mutation was introduced.

The baseline `pageAssembler.finish` profile attributes another approximately 1.04GiB to remeasuring candidate maps at its `measureWire` call. Scalar rows had just been serialized to enforce the page byte budget, but that length was not reused. The candidate caches it only when there is no hydration callback. A hydration callback can change values or introduce an encoding error; that path still serializes the final response. `TestRootPageRechecksResponseAfterHydration` injects a non-JSON `NaN` through the callback and requires final serialization to reject it. Preallocating the known upper bound of scan destinations removes repeated slice growth without changing column order.

The full process profiles report 10,203.78MiB before and 4,750.98MiB after in sampled allocated bytes. `pageAssembler.finish` no longer has sampled allocation at the former duplicate `measureWire` call in this fixture. Those cumulative totals include setup and warmup and are not peak memory or per-request values. The per-operation benchmark columns are the appropriate allocation comparison. JSON decoding, response maps, numeric conversion, policy/projection work, and PostgreSQL protocol handling remain real costs; this change does not make page reads allocation-free.

CPU samples total 31.13 CPU-seconds before and 21.92 CPU-seconds after across the full respective query processes. `EncodeRecord` cumulative CPU samples are 8.41s and 5.93s. These are sampled Go process costs, including setup and warmup; PostgreSQL backend CPU is outside the profiles. The result does not establish total host CPU savings at a fixed production arrival rate.

The lower allocation did not produce uniform latency improvement. First-page throughput at concurrency 1 fell from 331.6 to 240.7 queries/s. Filtered-page throughput at concurrency 8 fell from 1,026 to 878.5, and at concurrency 32 from 991.8 to 769.8. Several other cases improved. Mixed HTTP throughput rose at 1, 8 and 32 workers but fell from 532.41 to 522.04 requests/s in the final four-worker stage. All four HTTP p99 values increased, including 162.14ms to 227.64ms at 32 workers. These regressions remain in the table and raw evidence. There is no stable p95/p99 improvement claim from one pair.

The smaller mixed HTTP reduction must be interpreted on its own: this fixture has three fields, relation hydration, user authentication, mutations and client-side parsing, while the native profile has twenty scalar fields. The native 53% page allocation reduction is not a 53% HTTP allocation or throughput result.

The helper microbenchmark retained the old copy/sort implementation in the same process as a reference. On already ordered 20-field metadata it reported 8,680B and four allocations for the reference versus zero bytes and allocations for the current path. The broader 20-field codec microbenchmark reported approximately 14,762B/96 allocations before and 5,745B/89 allocations after. Those preliminary microbenchmark timings were collected during toolchain/test activity and are not used as paired latency evidence. The native profiles above provide the production-path attribution and the measured page allocation comparison.

## HTTP contracts, pool saturation and recovery scope

The HTTP fixture creates two authenticated owners, two related groups, and 128 seeded notes per owner. Notes have indexed `title` and integer `rank` fields plus a single relation to the owning group. Requests use `rank < 1000`, `sort=rank,title`, `expand=group`, page size 50, and a native continuation cursor. Each worker's transient mutation uses rank 1,000,000, so it does not destabilize the filtered seed pages.

Every ten-request cycle performs create, ETag-guarded update, ETag-guarded delete, one point read, five first-page reads and one continuation read. Each stage runs 100 cycles, producing the 30% mutation / 70% read mix. The recorded pair checks response status, mutation identity and ETag progression, updated values, owner visibility, relation identity, required continuation, and first/second-page non-overlap. Before and after every stage it verifies both owners still see exactly 128 notes, cross-owner point reads return 404, and the physical notes table still has exactly 256 rows.

Independent review identified a narrow test assertion gap: the original paired harness did not explicitly require rank/title order or unique IDs within each page. Commit `15b19c36` adds those checks against the deterministic fixture: ranks 0–49 on first pages, 50–99 on continuation, owner-specific exact titles and per-page unique IDs. A separate 32-cycle run passed all four stages, 1,280 requests and zero errors in 5.607s. Its timing is functional smoke evidence, not an additional before/after sample. Original paired metrics remain attributed to `8d0ceddb` because the extra client-side validation work is inside the whole-stage resource window.

At 32 workers the four-connection HTTP pool experienced 3,351 waits before and 3,328 after across each 1,000-request stage, with cumulative wait times of 57.90s and 52.44s. A request can acquire a connection more than once, so waits can exceed request count. Wait durations sum across concurrent acquisitions and can exceed wall time. All measured stages had zero canceled acquisitions and zero unexpected HTTP errors. Native concurrency-32 cases had approximately 0.991–0.992 waiting acquisitions per operation. These observations demonstrate exercised pool pressure, not an admission-capacity limit.

The final four-worker stage confirms the same fixture remains usable with correct counts after a higher-concurrency stage. It is not a crash, network outage, storage failure or database-restart recovery test. Heap snapshots and allocation counters cover the server and in-process HTTP client, including validation and background application work; they do not measure isolated server RSS, peak memory or retained-heap growth. No forced-GC normalization was applied between stages.

## PostgreSQL execution-plan evidence

The benchmark explains the actual `rootPlan.pageStatement`, including the current membership high-water predicate, rather than a hand-written approximation. Both baseline and candidate plans were captured on separate 500,000-row disposable fixtures using `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`. The later one-iteration diagnostic timings are excluded from the paired performance table.

| Case | Observed plan on both revisions | Actual result | Remaining cost |
| --- | --- | ---: | --- |
| First page | `Limit` → nested loop with root-table index scan and `record_creation_ordinals_pkey` lookup | 51 roots, 51 membership lookups | One lookahead row is intentional; no full root-table scan or temporary spill in these plans |
| `priority = 42` | Same ordered root index scan plus membership primary-key lookup | 51 roots after rejecting 4,991 nonmatching roots | The fixture lacks a filter/order composite index, so it scans 5,042 ordered roots for this page |

Both plans report zero temporary read/write blocks. The raw plans retain actual buffer hits, reads, filters, planning time and execution time. The relevant candidate root index is unchanged by the Go allocation patch. The filtered plan is evidence of an application-specific indexing opportunity, not grounds to add an unsolicited global index or claim that the current memory patch improved PostgreSQL planning. A future workload-specific comparison can test an appropriate filter-plus-order index against its write/storage cost.

## Realtime component qualification

The main agent independently exercised the existing application-message broker with `BenchmarkApplicationRealtimeFanout`, using a benchmark-only addition at local integration commit `7480cd3bea421d122b9467e53e753b30b85afdb2`. Runtime broker code is unchanged from the baseline. One in eight clients has an already full queue; fast queues are drained synchronously after each publish. The benchmark asserts the delivered count and payload content on every operation. The three samples use 300ms benchmark duration with `GOMAXPROCS=4`; other agent work was active, and there is no before/after comparison for this component.

| Connected queues | Already full queues | Delivered per operation | Median ns/operation, three samples | B/op | Allocations/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 0 | 1 | 432.9 | 48 | 1 |
| 32 | 4 | 28 | 5,068 | 1,880 | 4 |
| 256 | 32 | 224 | 34,574 | 13,658 | 4 |

This measures in-process `Publish` plus draining fast queues, not network Realtime throughput, PostgreSQL replay, authorization-query cost, end-to-end latency or delivery guarantees. The already saturated queues deliberately receive no new message. A separate `go test -race ./internal/api -run '^TestRealtime' -count=1` run passed 21 top-level Realtime tests in 18.755s, including slow-consumer admission release, reconnect/replay order, verified policy context, revocation/unsubscribe and write-timeout handling. That suite supplies behavioral transport evidence; it does not turn the queue microbenchmark into an end-to-end load test. Raw outputs and all three samples are retained under the evidence directory.

## Correctness checks and independent review

Full non-integration query/cursor unit suites passed. Focused cursor/query race tests passed three repetitions. Four PostgreSQL 17.9 integration cases passed under the race detector in 74.423s: bounded retention for plain rows, bounded retention for large scalar roots, serialization after hydration, and CRUD precision/constraints/pagination. The paired workloads and the separate stronger HTTP smoke passed as described above. These are local results for the stated source revisions; validation of the combined candidate and other PostgreSQL versions is tracked separately in [validation.md](validation.md).

The Security Agent independently reviewed the field-order borrowing, destination capacity, scalar byte-count reuse and cursor guards, found no blocker, and separately ran focused query/cursor race tests. All inspected `fieldsByOrdinal` callers only read the returned slice. Future callers must retain that read-only contract. The same agent independently reviewed the load sample-count/lifecycle/response-bound logic and identified the page assertion gap subsequently fixed in `15b19c36`; its follow-up review found no blocker. Reciprocal review of the MFA, archive-size and contextual-mail patches included an independent two-repetition focused race execution across `adminauth`, `backup`, `mailer` and `userauth`.

These changes preserve public API behavior, cursor format, page bounds and catalog schema version. No migration is needed for the query allocation change. They do not establish a complete authorization, concurrency, architecture or operational audit of the entire platform.

## Reproduction and remaining work

Use an isolated PostgreSQL administrative URL with permission to create/drop only disposable test databases. PostgreSQL must have `pg_trgm`. The fixture creates unique database names and removes them when the run completes. Run the following against each listed runtime revision after copying the same benchmark/test harness into both trees; do not copy candidate runtime files into the baseline. To reproduce the original paired HTTP numbers use the original `8d0ceddb` harness in both trees. The current stronger harness is appropriate for ongoing qualification but includes additional assertion work.

```sh
export GOMAXPROCS=4
export PGBASE_TEST_ADMIN_URL='postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable'
export PGBASE_BENCH_ROWS=500000
export PGBASE_BENCH_EXPLAIN=1
pgbase_profile_dir=$(mktemp -d)

go test -tags=integration ./internal/query -run '^$' \
  -bench '^BenchmarkConcurrentRecordReads$' -benchtime=1000x -count=1 -v -timeout=15m \
  -memprofile="$pgbase_profile_dir/query.mem" -cpuprofile="$pgbase_profile_dir/query.cpu" \
  -o "$pgbase_profile_dir/query.test"

PGBASE_MIXED_CYCLES=100 go test -tags=integration,load ./pgbase \
  -run '^TestMixedHTTPPoolQualification$' -count=1 -v -timeout=10m \
  -memprofile="$pgbase_profile_dir/mixed.mem" -cpuprofile="$pgbase_profile_dir/mixed.cpu" \
  -o "$pgbase_profile_dir/mixed.test"

go tool pprof -top -alloc_space "$pgbase_profile_dir/query.test" "$pgbase_profile_dir/query.mem"
go tool pprof -top "$pgbase_profile_dir/query.test" "$pgbase_profile_dir/query.cpu"
```

The original paired native command omitted `-v`: its actual plans executed but parent benchmark logs were suppressed. The retained plans were therefore collected by a separate command using the same fixture and `-v -benchtime=1x -bench '^BenchmarkConcurrentRecordReads$/rows=500000/list_50/concurrency=1$'`. Those one-sample outputs are not used as p50/p95/p99 evidence. Including `-v` in future paired runs retains the plan logs directly.

To repeat the focused correctness checks:

```sh
go test ./internal/query ./internal/cursor -count=1
go test -race ./internal/cursor ./internal/query \
  -run 'TestCursorEnvelopeSizeBoundary|TestV5DescriptorMatches|TestRecordCodecKeeps|TestEncodeRecord|TestRelationCodec|TestPageAssembler' -count=3

go test -race -tags=integration ./internal/query \
  -run '^(TestReadKernelPlainListRetainsOnlyEmittedRootsAndOneLookahead|TestReadKernelLargeScalarRootsRetainExactlyEmittedPlusOneMap|TestRootPageRechecksResponseAfterHydration|TestRecordServiceCRUDConstraintsPrecisionAndPagination)$' \
  -count=1 -timeout=10m

PGBASE_MIXED_CYCLES=32 go test -tags=integration,load ./pgbase \
  -run '^TestMixedHTTPPoolQualification$' -count=1 -v -timeout=10m

go test ./internal/api -run '^$' -bench '^BenchmarkApplicationRealtimeFanout$' \
  -benchmem -benchtime=300ms -count=3
```

The managed local environment does not keep a background PostgreSQL daemon accessible after its owning execution command exits. Its first attempt failed with connection refusal; the next failed before product assertions because `pg_trgm` was absent. Final local tests start the private server, run all dependent tests and stop it within one command lifetime. Those environment failures are excluded from success totals and performance data.

Remaining qualification includes sustained and open-loop arrival patterns, hot-row contention, larger mixed relational datasets, production-shaped policy complexity, network latency and packet loss, storage fault injection, multi-instance recovery and end-to-end Realtime fanout with slow sockets. The four-worker stage is not failure recovery. PostgreSQL versions beyond 17.9, other operating systems and the combined post-integration source require their own recorded CI evidence. No arbitrary latency threshold was chosen after observing these results.

## Historical measurements retained from the earlier handoff

The following results were recorded by the earlier workstream and retain their original CI links. They are historical context and are not part of the new local before/after experiment. These are component measurements, not a capacity guarantee. Original source: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. All fixtures use disposable data. The paired microbenchmarks below used the same GitHub-hosted Ubuntu 24.04 runner for each comparison, AMD EPYC 9V74, four logical CPUs and Go 1.26.11. The concurrent query profile used a different Intel runner, identified separately.

### Compiled quota exclusions

Evidence: [run 43747074247](https://github.com/yakisoba0728/PgBase/actions/runs/43747074247), job `132912644174`, candidate `2d0c26170263911cf713504b0b43fe4f42903ca4`. The old parsing algorithm is retained only as a benchmark reference so both paths execute in the same process. The client address matches the final CIDR. Medians are from five 300ms samples.

| CIDRs | Baseline ns/op | Compiled ns/op | Baseline B/op / allocations | Compiled B/op / allocations |
| --- | ---: | ---: | ---: | ---: |
| 1 | 42.88 | 21.07 | 0 / 0 | 0 / 0 |
| 32 | 2116 | 153.5 | 1488 / 31 | 0 / 0 |
| 256 | 17771 | 1080 | 12240 / 255 | 0 / 0 |

Parsing and immutable snapshot construction move to `Set`; this is a request-path tradeoff, not free computation. PostgreSQL quota writes are unchanged. The 13.8x result for 32 CIDRs is **not** a 13.8x API throughput result. The same run reproduced the masked database error on baseline and passed five corrected integration/race repetitions.

### Bounded thumbnail byte pipeline

Evidence: [run 43747833767](https://github.com/yakisoba0728/PgBase/actions/runs/43747833767), job `132914949051`, candidate `46a479ccbf978efb82573f8aac7c4eb27d474755`. The same benchmark file ran on baseline and candidate. It decodes a valid 1x1 WebP and produces a 16x16 PNG; the second fixture adds a 1MiB RIFF JUNK chunk to isolate duplicate encoded-byte buffering. Medians are from five 300ms samples.

| Input | Baseline median ns/op | Candidate median ns/op | Baseline B/op / allocations | Candidate B/op / allocations |
| --- | ---: | ---: | ---: | ---: |
| Small WebP | 193141 | 195339 | 858923 / 47 | 859843 / 46 |
| WebP + 1MiB metadata | 1132288 | 142952 | 5332841 / 103 | 859851 / 47 |

Small-image latency is effectively unchanged (the median is about 1.1% slower); do not present this as a general image speedup. The metadata-heavy fixture uses about 83.9% fewer allocated bytes and is about 7.9x faster. It is intentionally synthetic and does not measure large decoded pixel arrays, image quality, object-store/network latency or total service RSS.

Raw ns/op samples:

- Small baseline: 193141, 196025, 191782, 191595, 193288.
- Small candidate: 195339, 195867, 182502, 191547, 196836.
- Metadata baseline: 1159844, 1122032, 1132288, 1152048, 1051881.
- Metadata candidate: 142093, 144431, 145870, 142952, 141593.

Focused image contracts passed five race-detector repetitions. The same run completed a 20-second compression-negotiation fuzz run: 110087 executions, no failure. This is bounded fuzz evidence, not proof that every malformed HTTP input is safe.

### Concurrent query profile: 500000 rows

Evidence: [run 43750171585](https://github.com/yakisoba0728/PgBase/actions/runs/43750171585), job `132922163590`, source `5af9dadf7a2ce2ff614f00301541566d7d0258ed`. Artifact `14175504859` contains the original console results and environment. GitHub-hosted Ubuntu 24.04.5, **Intel Xeon Platinum 8573C, four logical CPUs**, Go 1.26.11, pinned PostgreSQL 17.9 Bookworm container. No production endpoint was used.

The database has 500000 rows with 20 fields. The fixture verifies the physical row count, creation-ordinal count and native query count. Each workload warms five times, then executes 1000 measured operations at each of concurrency 1, 8 and 32: **12000 measured operations, zero errors**. Pages contain 50 records; the deep cursor starts after walking 10000 rows; the filter is `priority = 42`.

| Workload | Concurrency | Queries/s | p50 ms | p95 ms | p99 ms | Allocated B/query | Allocations/query |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Get | 1 | 901.3 | 1.001 | 1.286 | 1.833 | 82801 | 445 |
| Get | 8 | 2476 | 2.739 | 3.909 | 21.93 | 83333 | 449 |
| Get | 32 | 2708 | 11.01 | 14.04 | 32.97 | 83166 | 448 |
| First page, 50 | 1 | 265.7 | 3.567 | 4.282 | 19.57 | 1117683 | 10746 |
| First page, 50 | 8 | 282.9 | 15.66 | 152.0 | 236.3 | 1118153 | 10751 |
| First page, 50 | 32 | 486.0 | 62.19 | 81.35 | 84.63 | 1117864 | 10750 |
| Deep cursor, 50 | 1 | 198.2 | 4.795 | 5.365 | 22.20 | 1126021 | 10866 |
| Deep cursor, 50 | 8 | 369.3 | 20.39 | 34.12 | 40.01 | 1126623 | 10872 |
| Deep cursor, 50 | 32 | 377.1 | 80.64 | 101.3 | 112.6 | 1126330 | 10871 |
| Filtered page, 50 | 1 | 235.0 | 3.996 | 5.034 | 21.07 | 1123890 | 10842 |
| Filtered page, 50 | 8 | 421.7 | 17.82 | 31.15 | 36.13 | 1124295 | 10847 |
| Filtered page, 50 | 32 | 431.4 | 70.94 | 88.69 | 93.16 | 1124128 | 10846 |

This is one **closed-loop query-service** profile, not an HTTP throughput result or a before/after speedup claim. It includes pool waiting and PostgreSQL work, but excludes HTTP/TLS/CDN, session parsing, response serialization, writes, complex relations, external storage and realtime. It does not correct coordinated omission or establish saturation capacity. The first-page p99 at concurrency 8 is worse than at 32 in this run: preserve that observation rather than claiming monotonic scaling from a shared runner.

At the original handoff, roughly 1.12MB and 10700 allocations per 50-row page were an unattributed profiling lead. The current experiment above reproduces and attributes that allocation, then measures the implemented reduction. Application-specific arrival rates, sustained mixed workloads and failure recovery remain qualification work.

#### Fixture correction retained as evidence

The first run (`43749493919`, source `9bdf45d6`) failed during deep-cursor preparation. Extra rows inserted directly by the benchmark did not have creation ordinals. This was a fixture bug, not a successful 500000-row run or a product cursor defect. Source `5af9dadf` uses the existing test-only sidecar seeding helper and verifies all three counts before measuring. That helper is not a supported production import path; applications must mutate through Record Operations.

### Reproduction

Use a disposable PostgreSQL administrative URL, never production:

```sh
go test ./internal/apiratelimit -run '^$' -bench '^BenchmarkExcludedIPPolicy$' -benchmem -benchtime=300ms -count=5
go test ./internal/filestore -run '^$' -bench '^BenchmarkThumbnailBytePipeline$' -benchmem -benchtime=300ms -count=5
PGBASE_BENCH_ROWS=500000 go test -tags=integration ./internal/query -run '^$' -bench '^BenchmarkConcurrentRecordReads$' -benchtime=1000x -count=1 -timeout=15m
```

`PGBASE_TEST_ADMIN_URL` must already refer to a disposable PostgreSQL server for the last command. The harness creates and drops a uniquely named database. The benchmark accepts multiples of 10000, capped at one million. Production qualification still requires a workload-specific dataset, arrival rate, latency/error budget, sustained mixed-load test, failure model and independently reviewed recovery evidence.
