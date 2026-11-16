# Final functional qualification — 14/14 required jobs passed

## Decision and exact source

**Functional CI execution: PASS.** Full source qualification
[43861850923](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923),
attempt 1, completed successfully at 2026-11-16T12:08:45Z
(2026-11-16 21:08:45 KST): **15/15 jobs successful**, comprising **14 functional
jobs and one performance job**, with no failed, cancelled, skipped, or unfinished
job in this run. Performance acceptance and independent security conclusions
have their separate owner reports; this document does not substitute for them.

- Frozen PR HEAD: `4091c3be20ec8db177886a2a85550924fce37ff5`.
- Actual checkout in all 14 retained functional job logs:
  `ba838872d72993abf1953dcafc64ba043f978c06`.
- Exact HEAD and checkout tree: `653a46758f9e6af9662b77ecfe2f069a2ac94cb4`.
  Both Git tree objects were read locally and `git diff --name-only` was empty.
- These are fresh results for the integrated listener correction and retained
  authorization, Jobs and SDK fixes. Historical green runs were not reused.

## Actual current-source results

| Required area | Observed result | Job ID |
| --- | --- | --- |
| Go unit/static | Formatting, pinned lint, vet, race unit suite and private-attestation integration passed; 49 primary suite package pass summaries | 133280388096 |
| PostgreSQL 16.13 | 51 primary integration package pass summaries; isolated MinIO, matching client tools, SDK/live HTTP and clean distribution checks passed | 133280387312 |
| PostgreSQL 17.9 core | 50 primary integration package pass summaries under race; full integration vet, bounded multiprocess durability and administrator SDK actual HTTP passed | 133280387368 |
| PostgreSQL 17.9 application | `./pgbase` race integration passed, 370.438 seconds; original 20-minute per-package timeout unchanged | 133280387341 |
| PostgreSQL 18.3 | 51 primary integration package pass summaries; isolated MinIO, matching client tools, SDK/live HTTP and clean distribution checks passed | 133280387317 |
| SDKs | On each PG16/17-core/18 job: TypeScript 119/119, Dart 138/138, Dart analysis clean, five live SDK HTTP parent tests plus four subtests; browser example modules 2/2; committed TypeScript distribution reproduced exactly | Above three core matrix jobs |
| Administrator frontend | 78 files / 449 tests passed; security examples 4/4, typecheck, clean embedded build and exact committed-output check passed | 133280387146 |
| Browsers and recovery | Chromium 26/26; Firefox and WebKit 12/12 combined; hardened nonroot/no-shell runtime passed; black-box 30 top-level executions (15 distinct scenarios twice) plus 28 subtests passed | 133280387178 |
| Source analysis | Both complete configured CodeQL source jobs succeeded; raw findings remain subject to independent classification | Go 133280387167; JS/TS 133280387183 |
| Supply chain/image | Reachable Go scan, npm audit, local production-image build, SBOM, runtime-assets record, full-severity SARIF and unchanged HIGH/CRITICAL gate succeeded | 133280387112 |
| macOS | Native CLI/terminal vet and race validation passed | 133280387081 |
| Windows | Native self-update, CLI, durablefile, filestore and backup packages passed | 133280387001 |
| Observability | Dashboard JSON, alert rules, PromQL and actual metric contract passed | 133280387248 |
| Release artifacts | Existing immutable phase artifacts built and process rollout/race gate passed; no release or package publication | 133280386955 |

All 14 logs report **zero named test failures or skips**. Individual successful
Go test events in the primary unit/integration suites are suppressed by the
existing `testskip` tool, so an exact individual-test total cannot be recovered
from those logs. The package counts above are actual emitted pass summaries;
they are not invented test totals. Detailed excerpts preserve line numbers.

Existing matrix conditions still skip duplicate SDK/benchmark work in the
PG17 application shard and reserve extra administrator/multiprocess tests for
PG17 core. Those tests actually passed in the core job. Release-image-only
steps, cache-miss exports, JavaScript's Go-only extraction steps and failure-only
artifact uploads also correctly skipped. `functional-result.json` lists every
such step and asserts every required functional step succeeded. No assertion,
timeout, skip policy, browser filter or required-check setting changed.

## Narrow CI correction and trigger

Published correction
[`cb7247f47c52bf8b6a8160b785d46ad4f486db06`](https://github.com/yakisoba0728/PgBase/commit/cb7247f47c52bf8b6a8160b785d46ad4f486db06)
changes one reusable-workflow call in `full-qualification.yml`: the complete
qualification now uses the existing broader `nonperformance-e2e.yml`. The
previous full caller selected only Chromium. The selected workflow preserves
the original Chromium/hardened/black-box commands and includes the already
required Firefox/WebKit smoke projects and failure-trace retention. Agent C
independently approved this exact diff and callable input contract before freeze.

The coordinator removed the old nonperformance opt-in label before the final
normal-message publication, then added `full-qualification` once. Duplicate
nonperformance/ABBA wrappers skipped as intended. Three automatically triggered
routine workflows independently succeeded on the same HEAD; their metadata is
recorded in `parallel-routine-runs.json` as additional checks, not substitutes
for the final full run. No final run/job was retried.

## Durable raw evidence

- `run.json` and `jobs.json`: actual final run and complete job/step outcomes.
- `source-verification.json`: exact local Git object and empty-diff check.
- `functional-result.json`: 14 jobs, required-step checks, actual source and
  logged package/verbose-test counts; all conditional skips remain visible.
- `logs/*.log.gz`: all 14 original connector-retrieved UTF-8 job logs,
  losslessly compressed with gzip mtime 0; **4,605,259 raw bytes / 666,670 gzip
  bytes**. Every compressed stream was decompressed and compared to the
  retrieved bytes.
- `raw-log-manifest.json`: raw UTF-8 and compressed SHA-256, byte sizes and
  GitHub per-job log URLs. These hashes identify decoded log bytes, not an
  unrequested GitHub log ZIP.
- `functional-log-excerpts.json`: numbered result lines from the retained raw
  logs. `extract-functional-evidence.py` reproduces these derived records.
- `artifact-api.json`: all 10 run artifacts, their exact API-provided SHA-256,
  size, source binding, URLs and expiry. The release-phase artifact upload
  receipt matches API SHA-256
  `3a7c5df6be0acc18573afe8d84f212421ecc2ff8c48f92751083dcffd2a83605` at
  [artifact 14223885964](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14223885964).
  API/upload receipt agreement does not claim that this agent downloaded the
  51 MB release binary ZIP. Agent C separately recomputes security artifact
  ZIP hashes; Agent B separately preserves/recomputes load evidence.

Most raw CI artifact retention expires on 2026-11-30; SBOM, image SARIF and
release-phase artifacts expire on 2027-02-14 according to the retrieved API.
The compressed job logs and compact ledgers committed here remain available
independently of those artifact retention periods.

## Remaining limits

The historical Firefox font-abort cause is still unproved. It did not recur in
this mandatory current-source run, and no filter or speculative fix hid it.
Successful CodeQL execution does not mean zero findings; use Agent C's fresh
raw-finding reconciliation. Existing external-provider, Storage ambiguous-write
and writer-quiescence limits, and external Jobs-effect idempotency/fencing
requirements remain documented. This is one complete frozen-source CI
qualification, not an unrestricted production reliability claim.

Agent A authored the workflow wiring correction and verified execution/evidence;
Agent C supplies its independent review. No test process owned by Agent A
remains running. No main write/merge, force push, production access, deployment,
release, or package publication was performed.
