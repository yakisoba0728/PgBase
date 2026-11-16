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
- Closed-loop mixed final fixture and fault/multiprocess/realtime verification have independent correctness gates. Long-term soak, cross-region networks and production-capacity certification remain unproven unless actually run and recorded.

Final integrated runtime must be qualified separately from the historical A/B reconstruction. Changes to this protocol must be dated and explained rather than silently fitted to results.
