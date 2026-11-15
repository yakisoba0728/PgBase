# Measured performance and qualification boundaries

These are component measurements, not a capacity guarantee. Original source: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. All fixtures use disposable data. The paired microbenchmarks below used the same GitHub-hosted Ubuntu 24.04 runner for each comparison, AMD EPYC 9V74, four logical CPUs and Go 1.26.11. The concurrent query profile used a different Intel runner, identified separately.

## Compiled quota exclusions

Evidence: [run 43747074247](https://github.com/yakisoba0728/PgBase/actions/runs/43747074247), job `132912644174`, candidate `2d0c26170263911cf713504b0b43fe4f42903ca4`. The old parsing algorithm is retained only as a benchmark reference so both paths execute in the same process. The client address matches the final CIDR. Medians are from five 300ms samples.

| CIDRs | Baseline ns/op | Compiled ns/op | Baseline B/op / allocations | Compiled B/op / allocations |
| --- | ---: | ---: | ---: | ---: |
| 1 | 42.88 | 21.07 | 0 / 0 | 0 / 0 |
| 32 | 2116 | 153.5 | 1488 / 31 | 0 / 0 |
| 256 | 17771 | 1080 | 12240 / 255 | 0 / 0 |

Parsing and immutable snapshot construction move to `Set`; this is a request-path tradeoff, not free computation. PostgreSQL quota writes are unchanged. The 13.8x result for 32 CIDRs is **not** a 13.8x API throughput result. The same run reproduced the masked database error on baseline and passed five corrected integration/race repetitions.

## Bounded thumbnail byte pipeline

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

## Concurrent query profile: 500000 rows

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

The roughly 1.12MB and 10700 allocations per 50-row page are a profiling lead, not yet an attributed bottleneck. No arbitrary latency threshold was set after seeing the results. Application-specific arrival rates, sustained mixed workloads and failure recovery remain qualification work.

### Fixture correction retained as evidence

The first run (`43749493919`, source `9bdf45d6`) failed during deep-cursor preparation. Extra rows inserted directly by the benchmark did not have creation ordinals. This was a fixture bug, not a successful 500000-row run or a product cursor defect. Source `5af9dadf` uses the existing test-only sidecar seeding helper and verifies all three counts before measuring. That helper is not a supported production import path; applications must mutate through Record Operations.

## Reproduction

Use a disposable PostgreSQL administrative URL, never production:

```sh
go test ./internal/apiratelimit -run '^$' -bench '^BenchmarkExcludedIPPolicy$' -benchmem -benchtime=300ms -count=5
go test ./internal/filestore -run '^$' -bench '^BenchmarkThumbnailBytePipeline$' -benchmem -benchtime=300ms -count=5
PGBASE_BENCH_ROWS=500000 go test -tags=integration ./internal/query -run '^$' -bench '^BenchmarkConcurrentRecordReads$' -benchtime=1000x -count=1 -timeout=15m
```

`PGBASE_TEST_ADMIN_URL` must already refer to a disposable PostgreSQL server for the last command. The harness creates and drops a uniquely named database. The benchmark accepts multiples of 10000, capped at one million. Production qualification still requires a workload-specific dataset, arrival rate, latency/error budget, sustained mixed-load test, failure model and independently reviewed recovery evidence.
