# First full qualification: backend and SDK evidence

## Scope and source identity

This review reads the actual logs and step conclusions for run [43777701630](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630), rather than treating successful job badges as test counts.

| PostgreSQL | Job | Conclusion | Integration selection |
| --- | --- | --- | --- |
| 16.13 | [133007664032](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664032) | completed / success | `./...`, no race flag |
| 17.9 core | [133007664129](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664129) | completed / success | `./...` excluding `./pgbase`, with `-race -timeout 20m` |
| 18.3 | [133007664153](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664153) | completed / success | `./...`, no race flag |

All three jobs checked out **`e719d9d31655d4e6e4724bbb1dda5007235bc7e5`**, the PR merge of head `7619dbf790c9ccf88079247482b5db18fe8f018a` into base `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`. Evidence: PG16 and PG17 log lines 251–254; PG18 lines 246–249. The checkout identity is distinct from the runner image's own commit printed near the beginning of each log.

The reviewed jobs use Go 1.26.11, Dart 3.13.3, and Node 24.22.2. Evidence: PG16 lines 295, 363, 653; PG17 lines 295, 363, 686; PG18 lines 290, 358, 647. PostgreSQL container image versions are visible at lines 62 of PG16/PG17 and line 62 of PG18.

The separate PostgreSQL 17.9 `./pgbase` race shard is outside this three-job review. These core-job results alone do not establish that shard's result. Browser E2E, CodeQL, image scanning, and performance claims require their own job/artifact review.

## Retained evidence

The large raw job logs remain local scratch evidence and are not included in this repository; the cited GitHub jobs and the source-provenance manifest are retained links. The compact all-SDK follow-up log is included here.

The following are the GitHub connector's **decoded job-log text re-encoded as UTF-8**, preserving its BOM and newlines. The SHA-256 values identify these exact retained text files, not an Actions ZIP or an unobserved upstream HTTP body. Line references below use these files, including the BOM-bearing first line. Raw log environment values are not copied into this summary.

| Local file under `evidence/qualification/` | Bytes | Lines | SHA-256 |
| --- | ---: | ---: | --- |
| `job-133007664032.decoded.log` | 903348 | 5558 | `0884825c821cc9d3ab32c6aa7106aa250b04911002700fbfbed1254e2d891b51` |
| `job-133007664129.decoded.log` | 800412 | 5115 | `7d2df023e59c37d207cc87e0599f01b544e58331b0172e4d38dc9e3b587a8229` |
| `job-133007664153.decoded.log` | 838169 | 5148 | `5ed9e5b8ec76e8ca9248031527479b4e467656a712c64df0a672946bda3848e0` |

The corresponding `job-<id>.steps.json` files retain the independently retrieved step conclusions. `first-runtime-backend-provenance.json` records the checked-out commit and Git blob SHAs for the workflow, skip policy, and relevant test sources fetched at that exact merge commit.

## Go integration and skip policy

The workflow invokes:

```bash
set -o pipefail
test_arguments=(-count=1)
# PG17 core only in these three jobs:
# test_arguments=(-race -timeout 20m "${test_arguments[@]}")
scripts/test-go-integration.sh "${test_arguments[@]}" 2>&1 | tee go-test.log
```

At the checked-out commit, [scripts/test-go-integration.sh](https://github.com/yakisoba0728/PgBase/blob/e719d9d31655d4e6e4724bbb1dda5007235bc7e5/scripts/test-go-integration.sh) resolves the selected packages and runs:

```bash
go test -json -tags=integration "$@" "${packages[@]}" |
  go run ./tools/testskip -allowlist tests/go-test-skip-allowlist.tsv
```

It preserves the `go test` exit code and also fails on a skip-checker error. The checker is not merely a grep for success: the exact [tools/testskip/main.go](https://github.com/yakisoba0728/PgBase/blob/e719d9d31655d4e6e4724bbb1dda5007235bc7e5/tools/testskip/main.go) source prints buffered output for named failing or skipped tests, discards output for named passing tests, and rejects named skips absent from the allowlist. Package-level `Action=skip` for packages with no test files is explicitly outside that policy.

The exact [allowlist](https://github.com/yakisoba0728/PgBase/blob/e719d9d31655d4e6e4724bbb1dda5007235bc7e5/tests/go-test-skip-allowlist.tsv) contains only `internal/schema`'s `FuzzRelationNameDeterminismAndLimits(/.*)?`, with the stated reason of invalid UTF-8 fuzz input. S3 or missing-database skips are not allowed.

| Job | Integration command block / final result line | Packages with passing tests | Packages with no test files | Observed named skip output | Unallowlisted skip failure |
| --- | --- | ---: | ---: | ---: | --- |
| PG16 | 449–632 | 51 | 12 | 0 | none; policy step succeeded |
| PG17 core | 511–645 | 50 | 12 | 0 | none; policy step succeeded |
| PG18 | 443–626 | 51 | 12 | 0 | none; policy step succeeded |

These numbers count the package summary lines inside the integration step only. They are **not individual Go test-case counts**. No raw per-test JSON artifact is available in this review, and the passing named events were intentionally suppressed by the checker, so a total named-case count is not inferred.

The matrix selection and race environment are explicit at PG16/PG17 lines 168–170 and PG18 lines 163–165. The conditional `-race` line appears in every printed shell script, but only the PG17 core environment enables it. PG17 also ran `go vet -tags=integration ./...` successfully (lines 382–383 and the step conclusion). PG16 and PG18 intentionally skipped that separate vet step.

## S3 fixture execution and skip distinction

All three jobs restored and loaded the local MinIO fixture image, started a job-scoped Compose project, created the test bucket, provided the resolved loopback endpoint to the integration step, and removed the fixture afterward.

| Evidence | PG16 lines | PG17 core lines | PG18 lines |
| --- | --- | --- | --- |
| Cached fixture image loaded | 423 | 485 | 418 |
| Fixture startup command | 424–425 | 486–487 | 419–420 |
| Container started and bucket created | 439–448 | 501–510 | 434–442 |
| Endpoint supplied to integration environment | 470 | 532 | 464 |
| `internal/filestore` package passed | 563: 10.078s | 572: 9.815s | 557: 21.673s |
| Fixture shutdown/removal | 1068–1088 | 1124–1144 | 1062–1082 |

The recorded step named **Export S3 fixture image for the cache** is skipped in all three jobs because the image was restored from cache. This is a workflow conditional, not a skipped S3 test. The start/stop fixture steps themselves succeeded.

The exact [S3 integration test](https://github.com/yakisoba0728/PgBase/blob/e719d9d31655d4e6e4724bbb1dda5007235bc7e5/internal/filestore/s3_integration_test.go) requires the endpoint and checks object put/read, range bytes and metadata, delete, and not-found behavior against MinIO. An absent endpoint would produce a named skip that the integration policy rejects. The endpoint evidence, successful filestore package, and absence of named skip output therefore support real local-fixture execution. They do not establish behavior against an external cloud S3 service.

## Standalone SDK suites versus live PgBase conformance

The standalone SDK results are repeated on each PostgreSQL job; they must not be added together as different coverage.

| Validation | PG16 | PG17 core | PG18 |
| --- | --- | --- | --- |
| Dart analyze | No issues, line 729 | No issues, line 762 | No issues, line 723 |
| Dart standalone tests | 130 passed, line 864 | 130 passed, line 897 | 130 passed, line 858 |
| TypeScript Node test-runner cases | 109 passed, 0 failed/cancelled/skipped, lines 1009–1015 | 109 passed, 0 failed/cancelled/skipped, lines 1042–1048 | 109 passed, 0 failed/cancelled/skipped, lines 1003–1009 |
| Live public-record SDK Go test | `ok ./pgbase 3.008s`, line 1017 | `ok ./pgbase 3.050s`, line 1050 | `ok ./pgbase 3.786s`, line 1011 |

The TypeScript command is `npm --prefix sdk/typescript test`, following `npm ci` and deletion of `sdk/typescript/dist`. The package script builds, checks public API types, runs the legacy `test/sdk.test.mjs` script, then runs the named Node test suites. The **109** count belongs to that final Node test-runner portion. The legacy script uses its own Node HTTP fixture server; its successful exit and type-check/build success are additional checks, not 109 live PgBase requests or 109 Go integration cases. Evidence: PG16 lines 865–897, PG17 lines 898 onward, PG18 lines 859 onward; exact package script is included in the provenance manifest.

Dart runs `dart pub get`, `dart analyze`, and `dart test` in `sdk/dart`. Its 130-test success is likewise the standalone Dart suite, not a count of live PostgreSQL-backed contract tests.

All three jobs separately execute this exact live-server command:

```bash
go test -tags='integration sdkconformance' ./pgbase \
  -run '^TestSDKPublicRecordHTTPConformance$' -count=1
```

Command evidence: PG16 line 869; PG17 line 902; PG18 line 863. The [matched Go test](https://github.com/yakisoba0728/PgBase/blob/e719d9d31655d4e6e4724bbb1dda5007235bc7e5/pgbase/sdk_integration_test.go) creates a real PgBase HTTP handler backed by a disposable PostgreSQL database, builds the TypeScript SDK, runs a Node consumer, and runs a Dart consumer. Missing tools or failing consumers cause test failure.

Its TypeScript assertions cover record creation, numeric/select/relation modifiers, atomic rejection of invalid modifiers, bound filters including filter-looking input, numbered pages and `skipTotal`, optimistic ETag conflict, file upload/download/delete, basic SSE replay, and logout/revocation. The Dart consumer covers typed modifier operations and atomic rejection, literal filter handling, native and numbered pages, and `skipTotal`. The two consumers have different assertion sets; the TypeScript file/SSE assertions are not attributed to Dart.

### Live coverage gap found in this first CI run

The exact test-name filter selects only `TestSDKPublicRecordHTTPConformance`. It does **not** run the other three existing `integration && sdkconformance` tests:

- `TestSDKRelationFileNativeListRegression`
- `TestSDKTextRecordIdentityHTTPConformance`
- `TestSDKRealtimeHTTPConformance`, including its TypeScript and Dart subtests

They are excluded from the ordinary `-tags=integration` run by their additional build tag. This is a concrete workflow coverage gap in run 43777701630, even though all reviewed jobs succeeded. Their later local execution is recorded separately below; it does not retroactively enlarge this run's CI coverage.

### Distribution and examples

Each job deletes the TypeScript dist directory, rebuilds through the normal test script, and then successfully checks `git status --porcelain --untracked-files=all -- sdk/typescript/dist`. This checks missing, changed, and unexpected generated outputs. Evidence: deletion at PG16 867 / PG17 900 / PG18 861; parity command at PG16 1049 / PG17 1082 / PG18 1043; successful parity step in each retained steps file.

The backend example-module step runs `npm --prefix examples/user-notes test` and reports **2 passed** in each job (PG16 1039–1046, PG17 1072–1079, PG18 1033–1040). This is a module-level test result, not a browser E2E result and not the separate JavaScript example's four security regressions.

## PG17 bounded multiprocess acceptance

Only PG17 core runs:

```bash
go test -tags='integration multiprocess' ./pgbase \
  -run '^TestMultiProcessBoundedFailureAcceptance$' -count=1 -v
```

Command: lines 646–647. Actual result: lines 661–665, with the measurement at line 662:

- 2 application processes against the disposable database.
- 32 unique concurrent create requests.
- 12 same-idempotency-key requests, returning **1 HTTP 201 and 11 HTTP 409** during contention.
- 35 durable records after verification.
- 1 process killed and restarted.
- Realtime resumed from the prior cursor.
- Cross-process session revocation verified.
- Named test passed in **3.22s**; Go package elapsed **3.234s**.

The [test source](https://github.com/yakisoba0728/PgBase/blob/e719d9d31655d4e6e4724bbb1dda5007235bc7e5/pgbase/multiprocess_acceptance_integration_test.go) verifies that subsequent same-key replays on both instances return the original ID, that a write on the surviving process is available after restart/SSE resumption, and that final durable records equal 32 unique + 1 idempotent + 2 failure-window records. This is a bounded correctness acceptance test, not a throughput, network-partition, or production-availability proof.

PG16/PG18 skip this **workflow step by design**. These two jobs do not supply additional multiprocess evidence. This command has no `-race`; the PG17 main integration step's race flag does not implicitly apply to this separate command.

## PG17 administrator SDKs against an actual server process

Only PG17 core runs:

```bash
go test -tags='integration multiprocess' ./pgbase \
  -run '^TestAdministrationSDKsAgainstRealHTTPServer$' \
  -count=1 -v -timeout 8m
```

Command: lines 1101–1102. Actual results, lines 1116–1123:

| Test | Result |
| --- | --- |
| `TestAdministrationSDKsAgainstRealHTTPServer` | PASS, 5.21s |
| `TypeScript_external_consumer` | PASS, 1.61s |
| `Dart_external_consumer` | PASS, 1.89s |
| Go package | `ok`, 5.215s |

The [Go harness](https://github.com/yakisoba0728/PgBase/blob/e719d9d31655d4e6e4724bbb1dda5007235bc7e5/pgbase/sdk_administration_acceptance_integration_test.go) builds and launches a real PgBase executable with a disposable database and backup directory, then invokes external TypeScript and Dart programs. Both programs must exit successfully and produce their exact success marker. The consumer sources verify administrator account/session operations, schema export/preview/apply with a committed operation receipt, backup creation through a terminal job state, and backup upload/download byte equality, listing, and deletion.

This acceptance test does not perform a destructive backup restore. Its backup checks must not be presented as end-to-end restore qualification. PG16 and PG18 skip this workflow step by design.

## Follow-up: all four live SDK tests on local PostgreSQL 17.9

After finding the CI selection gap, the root agent requested execution of all existing live SDK tests before widening the workflow. This is **additional local validation**, separate from run 43777701630.

- Integrated checkout HEAD: `4be0c52cc444e6796b675dd76fdf3970dc7fe32d`.
- PostgreSQL 17.9, Go 1.26.11, Dart 3.13.3, Node 24.22.1.
- New private PostgreSQL data directory initialized for this invocation; loopback-only TCP listener with Unix sockets disabled.
- Initialization, server start, tests, and server stop occurred within the same execution context.
- Dependencies resolved from local caches via `npm ci --ignore-scripts --offline` and `dart pub get --offline`.
- Command:

```bash
go test -tags='integration sdkconformance' ./pgbase \
  -run '^TestSDK' -count=1 -v -timeout 5m
```

| Named test | Observed result |
| --- | --- |
| `TestSDKPublicRecordHTTPConformance` | PASS, 3.12s |
| `TestSDKRelationFileNativeListRegression` | PASS, 1.45s |
| `TestSDKTextRecordIdentityHTTPConformance` | PASS, 14.82s |
| `TestSDKRealtimeHTTPConformance` | PASS, 16.63s |
| Realtime TypeScript subtest | PASS, 2.32s |
| Realtime Dart subtest | PASS, 3.99s |
| Package | PASS, 36.049s, process exit 0 |

Evidence: `sdk-all-live-pg17.log`, lines 1–5 identify the commit/runtime and lines 12–25 contain all named results. File SHA-256: `c98cad5ec977deced1beec5babbb0a6375ca01af51a7cb3da8282f057fb27e23` (1069 bytes, 25 lines). The private database shutdown log records `server stopped`. Post-test status shows no SDK source or distribution changes.

No product fix was necessary for these four tests. Root was notified that broadening CI to `-run '^TestSDK' -v` is supported by this local run; only a subsequent CI run can establish that broader selection on PostgreSQL 16/17/18.
