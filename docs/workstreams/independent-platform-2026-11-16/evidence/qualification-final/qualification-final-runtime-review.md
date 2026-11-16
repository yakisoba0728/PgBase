# Final runtime qualification: backend, SDK, browser and recovery review

Run: [43780150632](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632). Independently retrieved job conclusions, completed raw logs, and relevant source files. **All four jobs in this review completed successfully.**

- Runtime commit: `5e4b71d1f602c9c136809bf65f2375bf75fc4a9f`.
- Actual checkout in every reviewed job: PR merge `2d712ba6239f2d19f1329400848173842ed7874c`, merging that runtime into `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`.
- Machine evidence: `runtime-counts.json`, `runtime-provenance.json`, `runtime-job-conclusions.json`, and four `job-*.steps.json` files. These contain exact log line references, counts, selected result text, source blob SHAs, and hashes. No source files were changed for this review.

## Backend and SDK results

| PostgreSQL / job | Go packages passing | Packages without test files | Observed named Go skips | TS / Dart standalone cases | Live SDK result |
| --- | ---: | ---: | ---: | --- | --- |
| 16.13 / `133015260615` | 51 | 12 | 0 | 109 / 130 | 4 + 2 Realtime subtests |
| 17.9-core / `133015260618` | 50 | 12 | 0 | 109 / 130 | 4 + 2 Realtime subtests |
| 18.3 / `133015261053` | 51 | 12 | 0 | 109 / 130 | 4 + 2 Realtime subtests |

These Go numbers are **package summaries**, not individual test counts. The integration skip-policy step succeeded in all three jobs, with no named test failure/skip output or skip-checker error. The exact checker preserves named skip/failure output and rejects unallowlisted skips; its sole allowlist entry is the existing invalid-UTF-8 schema fuzz case. Twelve packages without test files are outside that named-skip policy.

PG17 core uses `-race -timeout 20m` and excludes `./pgbase`; PG16 and PG18 use `-count=1` without race. The separate PG17 application race shard was not part of this agent's assigned review. Its result must come from that job's independent evidence.

Every reviewed backend job reports TypeScript **109 passed, 0 failed/cancelled/skipped** and Dart **130 passed**, with Dart analysis reporting no issues. The TS command additionally builds, type-checks, and runs the legacy Node HTTP-fixture script before the 109 counted Node runner cases. These standalone results are distinct from the PostgreSQL-backed live tests and are repeated coverage across the matrix, not different cases to add together.

### Widened live tests actually executed

All three logs contain individual RUN/PASS entries for every test below and both Realtime language subtests. The command is:

```bash
go test -tags='integration sdkconformance' ./pgbase -run '^TestSDK' -count=1 -v
```

| Live test | PG16.13 | PG17.9 core | PG18.3 |
| --- | ---: | ---: | ---: |
| `TestSDKPublicRecordHTTPConformance` | 2.88s | 3.17s | 3.16s |
| `TestSDKRelationFileNativeListRegression` | 0.73s | 0.8s | 1.16s |
| `TestSDKTextRecordIdentityHTTPConformance` | 12.06s | 12.38s | 13.02s |
| `TestSDKRealtimeHTTPConformance` | 6.29s | 6.64s | 7.58s |

Actual named-result ranges: PG16 log lines **1024–1037**, PG17 **1047–1060**, PG18 **1011–1024**. This closes the earlier CI selection gap that ran only the public-record contract. It covers the relation/file list, text record identity, and live Realtime contracts in addition to public records. Both TypeScript and Dart Realtime subtests pass on each database version.

Each backend job also deletes the TypeScript distribution before its normal build and passes the tracked/untracked distribution parity check. The separate user-notes module tests report 2 passes per job; they are not browser E2E tests.

## S3 and PG17 operational acceptance

All three jobs started the local MinIO fixture, created the bucket, supplied the endpoint to integration tests, passed `internal/filestore`, and successfully stopped the fixture. Filestore package times were **9.429s / 9.789s / 16.565s** for PG16/17/18. No S3 test skip appeared. The **cache image export** step was skipped after a successful cache restore; this is a workflow conditional, not a skipped S3 integration test. This validates the local MinIO fixture, not an external cloud S3 service.

PG17 core additionally passed the bounded multiprocess test. Its actual result records **2 processes, 32 unique requests, 12 same-key requests (HTTP 201:1, 409:11), 35 durable records, one kill/restart, Realtime resumption, and cross-process revocation** (log line 657). This remains a bounded correctness test, not a production capacity or availability measurement.

The administrator SDK real-server acceptance test passed in **4.98s**, with external TypeScript **1.53s** and Dart **1.81s** consumers (lines 1129–1131). These exercise administrator/session operations, schema import receipts, backup creation, and backup upload/download/list/delete. They do not perform a destructive backup restore. These separate multiprocess/admin commands do not inherit the main integration step's `-race` flag; PG16/PG18 intentionally omit them.

## Browser, crash recovery and credential masking

E2E job [133015260580](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260580) reports **26 browser tests collected, 25 passed in 53.4s, 1 skipped** (lines 2253–2256). Playwright retries are disabled in the exact configuration.

The single existing exclusion is **creates a managed backup**, guarded by `PGBASE_BROWSER_MANAGED_BACKUPS !== "1"` with reason “requires a disposable managed-host fixture.” The authenticated spec has the same Git blob at the base and runtime commits: `b64e27714b9d8133d184686cbf85faaf97443a29`. The console uses a dot reporter and does not print the skipped test name; identifying this exclusion uses the exact source's sole skip guard and the observed one-skip count. No new skip was introduced. The configured projects include the JavaScript example destination-security browser contract, onboarding, and authenticated workflows.

The black-box command is `go test -tags=e2e ./tests/e2e -v -count=2 -shuffle=on`. Logs show **15 distinct top-level tests, each passing twice**: 30 top-level PASS events, plus 28 subtest PASS events, no named skips/failures; package elapsed **132.393s**. In particular, abrupt application and PostgreSQL termination/recreation passes twice (**13.52s and 13.51s**, lines 2333 and 2391). The test verifies both containers exit with SIGKILL status, both are replaced, the acknowledged record and idempotency receipt survive, and the uncommitted mutation does not. Readiness after database outage, graceful drain, Compose stop, and multi-instance consistency checks also pass in both repetitions. Fixture cleanup succeeds. This job uses a source-built local CI image and PostgreSQL 16.13; it is not a deployed-release qualification.

The later environment echoes contain each generated fixture credential key **11 times**: `PGBASE_POSTGRES_PASSWORD`, `PGBASE_SETUP_TOKEN`, `PGBASE_TOKEN_SECRET`, and `PGBASE_ADMIN_PASSWORD`. **All 44 observed values are exactly `***`; zero unmasked assignments were found.** First occurrences are lines 302–305 and last occurrences 2446–2449. The source registers each value with the runner's mask mechanism before exporting it. No credential values are copied into this report or machine summaries.

## Decoded-log provenance

The following SHA-256 values identify the connector's decoded text retained as UTF-8, including its BOM and newlines; they are not hashes of an Actions ZIP. Detailed line mappings and source provenance are in the machine evidence. Full raw logs need not be committed with this summary.

| Job | Bytes | Lines | SHA-256 of decoded log |
| --- | ---: | ---: | --- |
| `133015260615` | 911178 | 5617 | `8d6f62972a6bab994c30bfbff428cfe34330c611734787f3cbeb1d08f9f62c5d` |
| `133015260618` | 803664 | 5134 | `cca610e95adc9bc9528583a2bcb1c686c0b5ea57647fccabed509ff946c303ba` |
| `133015261053` | 896704 | 5409 | `9567d6fb3fcf6a7a668f53e2210b309ba727e9a4d0076d8b03b2e2b0f3c23e0f` |
| `133015260580` | 409443 | 2516 | `da45f3ba55bde8d7a99f8cd1563d5e726bb88d9e3819efbe155940c2bb8c4811` |

This review covers the four assigned jobs. Root separately reviews the remaining qualification jobs, including the application race shard, security scans, image checks, and performance artifacts. Successful finite tests do not establish unlimited workload, every failure mode, or production stability.
