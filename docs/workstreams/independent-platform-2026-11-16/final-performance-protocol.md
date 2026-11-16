# Final continuation: preregistered performance protocol

Recorded before the continuation's first measured run. The earlier single fixed-order pair is a hypothesis, not a causal result.

## Reconstruction and primary comparison

- A: published `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b`.
- B: A plus published cursor bound `312f1c614b4c0d08c32a922dfb4fb2040ebaa3b6` and allocation change `c0a37c385b5a49bbf6fd9cef624eace9b941482f`.
- Common native harness from `e2f78171e2233a32ab4420a107cb5715485a24a5`; common original HTTP harness from `7c2e82234e188d623181048692d78975438d81f6`. Increase native warmup from five to 100 operations identically. Record source hashes and the exact harness patch. The stronger current HTTP assertions receive separate final qualification.
- Go 1.26.11, PostgreSQL 17.9, `GOMAXPROCS=4`, max_connections=100, local disposable databases. 500,000 native rows, 20 fields, pages of 50, concurrency 1/8/32. Original HTTP fixture: 256 related records, two owners, pool=4, 100 ten-request cycles at concurrency 1/8/32/4.
- Precompile both binaries before timing. Each invocation creates/validates/analyzes its own database and drops it afterward. Same construction algorithm and value distribution, independent generated IDs; not byte-identical physical databases. No production data or credentials.
- Three ABBA blocks: `A B B A A B B A A B B A`; six independent invocations per arm and workload. Execute native and HTTP within each position. CPU/allocation profiles enabled identically. Do not compile or run this team's unrelated tests during measurement. The shared host still cannot establish dedicated hardware isolation.

## Decisions fixed before results

Primary endpoints: native list_50 at concurrency 1 p99 and throughput; mixed HTTP concurrency 32 p99 and throughput. All other workloads, p50/p95, allocations, GC pauses, pool waits, errors and plans must also be reported, including unfavorable results.

Report per-run observations, median, range and paired log ratios. Pair adjacent opposite-arm positions within each ABBA block, keeping B/A direction. Bootstrap pairs (fixed seed, 20,000 resamples) for 95% intervals. Run-level observations, not individual correlated requests, are the replication units. Six pairs have limited power; no-effect is not inferred from a nonsignificant result. Do not delete noisy runs. Any execution failure remains in the ledger and is investigated separately.

An observed severe regression is a >=25% p99 increase or >=15% throughput decrease with the paired 95% interval excluding no change. A confidence interval wholly within 0.90–1.10 supports only bounded +/-10% equivalence for that endpoint/fixture. Otherwise classify evidence as improvement, regression or inconclusive with its interval. These thresholds classify this experiment; they are not production SLOs. If the primary regression is repeatable, investigate CPU/allocation profiles, GC/scheduler, pool waits and actual SQL plans before changing runtime behavior.

## Additional qualification planned before measurement

- Query filter/order index experiment on the actual native plan: preserve identical logical responses and report scan counts, buffers, execution latency and index storage/write cost. Never add an index globally solely to improve a synthetic fixture.
- Final native scale experiment at at least 1,000,000 rows where resources permit; preserve limits and disclose actual row count.
- Final bounded open-loop HTTP test: fixed arrivals include queue delay in end-to-end latency, fail visibly on dropped/unfinished work, and verify correctness. Initial target 200 requests/s for 60 seconds, zero unexpected errors or cross-owner results, p99 <=250ms. This provisional target is scoped to the disposable local fixture and is not a general capacity promise. Also report service latency and scheduling delay separately to expose coordinated omission.
- Before the first open-loop execution, the hosted final qualification is also set to 200 requests/s for 600 seconds (120,000 intended arrivals), with the same zero-error/data-integrity and 250ms p99 gates. This ten-minute test is sustained bounded load, not a multi-day soak or production-capacity certification. Resource limits and source revision must accompany its result.
- Closed-loop mixed final fixture and fault/multiprocess/realtime verification have independent correctness gates. Long-term soak, cross-region networks and production-capacity certification remain unproven unless actually run and recorded.

Final integrated runtime must be qualified separately from the historical A/B reconstruction. Changes to this protocol must be dated and explained rather than silently fitted to results.

## Follow-up registered after the local ABBA results

The first twelve native and twelve mixed invocations completed with no test errors. Native list_50/c1 improved, but mixed HTTP/c32 p99 remains statistically inconclusive and HTTP/c8 throughput showed a small adverse interval. Do not close those findings from memory results.

A second, hosted experiment therefore increases each HTTP stage from 1,000 to 5,000 requests and keeps three ABBA blocks, identical CPU/allocation profiling, Go1.26.11, PostgreSQL17.9 and pool4. It retains the same comparison thresholds and run-level confidence method. `performance-qualification` runs the original isolated patch comparison; `final-performance-qualification` substitutes the final PR runtime and explicitly represents a broader code comparison. Each experiment has 240,000 measured HTTP requests. Hardware/platform data are captured, so results from different hosts are not pooled. The workflow does not assert a universal performance SLO or suppress unfavorable observations.

## Statistics-controlled native repetition, registered before its first run

Independent review found a fixture confound in the original twelve native runs: the untimed filtered EXPLAIN in position 04 scanned the 500,000-row membership sidecar and probed 500,000 roots, instead of inspecting approximately 5,042 roots. The sidecar had been bulk rebuilt without an explicit ANALYZE. Its cardinality estimate was much smaller than the actual table. Stale statistics are a supported explanation, but original pg_stats snapshots were not captured and the untimed plan cannot establish the plan used by every subsequent timed request. All original observations remain in the first experiment.

Before additional measurements, the common native harness now analyzes both the application table and `record_creation_ordinals`, verifies the exact first 50 filtered identities against an independently ordered SQL reference, and optionally records individual latency samples. The reference and ANALYZE warm the fixture; this is explicitly a warm-cache experiment. `scripts/repeat_native_regression.py` reconstructs the same historical A/B sources and runs all twelve native cases at 500,000 rows, 100 warmups and 1,000 measured operations each, in three ABBA blocks. The same six run pairs, primary endpoints and confidence/serious-regression criteria apply. Its results are kept separate from the original and hosted HTTP experiments.

The final-runtime scale/index diagnostic will use 2,000,000 rows and four positions: no index, index, index, no index. Each position runs the same twelve cases at 1,000 operations each, records both native EXPLAIN plans and index build/storage costs, and then executes a rolled-back synthetic 1,000-row priority update under EXPLAIN (ANALYZE, BUFFERS, WAL). This write probe measures SQL index maintenance, not end-to-end Record Operations throughput. Two observations per arm support only a descriptive comparison; no narrow confidence interval or universal index recommendation will be claimed. The index remains exclusive to a disposable fixture.
