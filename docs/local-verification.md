# Local verification and historical M2–M6 results

Current P8 acceptance, PR/CI and installation evidence is in [project status](project-status.md) and [P8 contracts](phase4-local-contracts.md). The workload numbers and 431-test UI result below describe the earlier M2–M6 snapshot and are not current totals.

These checks use a source build and disposable local resources. They do not qualify a production deployment or establish a production SLO.

## Repeatable commands

```sh
# Use a disposable PostgreSQL administrator URL for tests.
PGBASE_TEST_ADMIN_URL=... PGBASE_TEST_S3_ENDPOINT=http://127.0.0.1:59000 \
  GOTOOLCHAIN=go1.26.8 scripts/test-go-integration.sh -race -count=1
GOTOOLCHAIN=go1.26.8 go test -tags=restore ./tests/restore -run TestRestoreDrill -count=1
scripts/test-local-pitr.sh
scripts/build-local-artifacts.sh
PGBASE_TEST_ADMIN_URL=... GOTOOLCHAIN=go1.26.8 \
  go test -tags='integration load' ./pgbase -run TestLocalLoadStages -v -count=1
```

The S3 fixture is `tests/filestore/compose.yaml`. `scripts/test-s3-fixture.sh up` starts its MinIO service, runs its `create-bucket` service and prints the `PGBASE_TEST_S3_ENDPOINT` to use (port 59000 unless `PGBASE_TEST_S3_HOST_PORT` overrides it; `0` picks a free port); `scripts/test-s3-fixture.sh down` removes it. Set a distinct `PGBASE_TEST_S3_PROJECT` per concurrent run. The suite rejects unapproved skips, including omission of this fixture. The cross-build command writes six executables and `SHA256SUMS` under `.local/artifacts`; it does not sign, upload, tag, or publish them. Cross-compilation does not demonstrate execution on all six target systems.

## Observed short workload, 2026-09-08

Go 1.26.8, macOS arm64 host, loopback HTTP and Docker PostgreSQL 17.9; 100 records, authenticated owner-scoped list requests returning 50 records. Database and application run on the same workstation. Other development tests may share that host. This is a 30-second smoke workload, not a long-duration soak.

| Stage | Duration | Concurrent clients | Requests/s | Failures | p95 | p99 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline | 5s | 1 | 502.2 | 0 | 2.27ms | 3.55ms |
| Short steady run | 15s | 4 | 1430.2 | 0 | 3.29ms | 4.22ms |
| Spike | 5s | 16 | 2240.6 | 0 | 8.52ms | 9.60ms |
| Recovery | 5s | 4 | 1429.4 | 0 | 3.31ms | 4.06ms |

Each response is decoded and checked for 50 records. The test has no production latency threshold; failures are correctness failures. Multi-hour soak, network partitions across machines, real workload sizing, and cloud capacity remain external qualification work.

## Recovery boundaries

The restore drill drains the source application, dumps the complete database, copies its local file object directory into a separate restore directory, restores into an empty database, and verifies API reads and downloaded file bytes. Migration 13 and newer candidates exercise the file path; immutable older release candidates continue their supported database-only contract. The standalone WAL drill uses disposable PostgreSQL marker transactions to verify a named recovery point and promotion, including exclusion of a later committed write. It never connects to the local development project. Its measured result is written to `.local/verification/pitr.txt`.

A live database dump and a concurrently changing file directory are not an atomic backup. Drain every writer and file-cleanup worker before copying database and file state. Keep object versions when designing online PITR with external storage; the PostgreSQL-only marker drill does not prove that integration.

## Final local feature checks, 2026-09-09

Go unit/race and PostgreSQL 17.9 integration/race passed their functional packages; the integration event audit accepted all skips. Local MinIO exercised S3 upload/read/delete. A source-built restore candidate passed the complete PostgreSQL 16/17/18 matrix, including local file bytes and authenticated reads. The final structural counters and schema/migration race checks passed after the last reviewed fix.

The admin UI has 431 passing tests in 30 files, plus typecheck and embedded production build. Existing onboarding and authenticated Playwright suites passed (1 + 7 tests). The opt-in `local-features-chromium` project covers compound-index changes, filtered read-only views, mobile overflow, auth realm lifecycle, file upload/download/delete, the browser SDK notes example and scoped service-token issuance/revocation. All four local feature browser tests passed. It requires the local source app and the example server; run it against a disposable development project with no existing `notes` collection:

```sh
npm --prefix sdk/typescript ci
npm --prefix sdk/typescript test
npm --prefix examples/user-notes run serve
# In another terminal, after starting scripts/dev-local.sh and onboarding:
PGBASE_LOCAL_BROWSER_BASE_URL=http://127.0.0.1:8090 \
  PGBASE_ADMIN_EMAIL=... PGBASE_ADMIN_PASSWORD=... \
  npm --prefix ui/admin run test:e2e -- --project=local-features-chromium
```

The SDK was packed into a real tarball, installed into a separate temporary npm project, and imported as ESM. Its actual PgBase HTTP conformance includes CRUD, ETag conflict, file bytes, replay and realm logout:

```sh
PGBASE_TEST_ADMIN_URL=... GOTOOLCHAIN=go1.26.8 \
  go test -tags='integration sdkconformance' ./pgbase \
  -run '^TestSDKPublicRecordHTTPConformance$' -count=1
```

These checks do not constitute a complete assistive-technology or cross-browser accessibility audit. Provider callbacks and mail use local fixtures, and no public package or cloud deployment was created.
