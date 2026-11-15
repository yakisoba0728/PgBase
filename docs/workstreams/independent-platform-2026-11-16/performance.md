# Measured performance and qualification boundaries

These are component measurements, not a capacity guarantee. Original source: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. Runners are shared GitHub-hosted Ubuntu 24.04, AMD EPYC 9V74, four logical CPUs, Go 1.26.11. Medians below use five 300ms benchmark samples on the same runner for each comparison. No production traffic or data was used.

## Compiled quota exclusions

Evidence: [run 43747074247](https://github.com/yakisoba0728/PgBase/actions/runs/43747074247), job `132912644174`, candidate `2d0c26170263911cf713504b0b43fe4f42903ca4`. The old parsing algorithm is retained only as a test benchmark reference so both paths execute in the same process. The client address matches the final CIDR in each list.

| CIDRs | Baseline ns/op | Compiled ns/op | Baseline B/op / allocs | Compiled B/op / allocs |
| --- | ---: | ---: | ---: | ---: |
| 1 | 42.88 | 21.07 | 0 / 0 | 0 / 0 |
| 32 | 2116 | 153.5 | 1488 / 31 | 0 / 0 |
| 256 | 17771 | 1080 | 12240 / 255 | 0 / 0 |

Configuration parsing and immutable snapshot construction move to `Set`; this is a request-path tradeoff, not free computation. PostgreSQL quota writes are unchanged. The 13.8x result for 32 CIDRs is **not** a 13.8x API throughput result. The same run reproduced the masked database error on baseline and passed five corrected integration/race repetitions.

## Bounded thumbnail byte pipeline

Evidence: [run 43747833767](https://github.com/yakisoba0728/PgBase/actions/runs/43747833767), job `132914949051`, candidate `46a479ccbf978efb82573f8aac7c4eb27d474755`. The same benchmark file ran on baseline and candidate, on the same runner. It decodes a valid 1x1 WebP and produces a 16x16 PNG; the second fixture adds a 1MiB RIFF JUNK chunk to isolate duplicate encoded-byte buffering.

| Input | Baseline median ns/op | Candidate median ns/op | Baseline B/op / allocs | Candidate B/op / allocs |
| --- | ---: | ---: | ---: | ---: |
| Small WebP | 193141 | 195339 | 858923 / 47 | 859843 / 46 |
| WebP + 1MiB metadata | 1132288 | 142952 | 5332841 / 103 | 859851 / 47 |

Small-image latency is effectively unchanged (the median is about 1.1% slower); do not present this as a general image speedup. The metadata-heavy fixture uses about 83.9% fewer allocated bytes and is about 7.9x faster. It is intentionally synthetic and does not measure large decoded pixel arrays, image quality, object-store/network latency or total service RSS.

Raw ns/op samples:

- Small baseline: 193141, 196025, 191782, 191595, 193288.
- Small candidate: 195339, 195867, 182502, 191547, 196836.
- Metadata baseline: 1159844, 1122032, 1132288, 1152048, 1051881.
- Metadata candidate: 142093, 144431, 145870, 142952, 141593.

The focused image contracts passed five race-detector repetitions. The same run completed a 20-second compression-negotiation fuzz run: 110087 executions, no failure. This is bounded fuzz evidence, not proof that every malformed HTTP input is safe.

## Concurrent query profile

`BenchmarkConcurrentRecordReads` prepares a disposable 20-field database, warms each workload and measures get, first-page, deep-cursor and filtered reads at concurrency 1, 8 and 32. `PGBASE_BENCH_ROWS` controls rows in multiples of 10000, capped at one million. The intended qualification run uses 500000 rows and 1000 operations per case. It reports p50/p95/p99 latency, queries/second, allocations and errors. Any failed operation fails the run.

This is closed-loop query-service load: it includes pool waiting and PostgreSQL work but excludes HTTP, TLS/CDN, session parsing, response serialization, writes, complex relationship expansion, external storage and realtime delivery. It is not an open-loop saturation test and does not correct for coordinated omission. The run has no arbitrary performance pass threshold. At this revision its results are **pending**, not a claimed speed improvement over baseline.

## Reproduction

Use a disposable PostgreSQL administrative URL, never production:

```sh
go test ./internal/apiratelimit -run '^$' -bench '^BenchmarkExcludedIPPolicy$' -benchmem -benchtime=300ms -count=5
go test ./internal/filestore -run '^$' -bench '^BenchmarkThumbnailBytePipeline$' -benchmem -benchtime=300ms -count=5
PGBASE_BENCH_ROWS=500000 go test -tags=integration ./internal/query -run '^$' -bench '^BenchmarkConcurrentRecordReads$' -benchtime=1000x -count=1 -timeout=15m
```

`PGBASE_TEST_ADMIN_URL` must already refer to the disposable PostgreSQL server for the last command. The harness creates and drops a uniquely named database. Production qualification still requires a workload-specific arrival rate, dataset, latency/error budget, sustained mixed-load test, failure model and independently reviewed recovery evidence.
