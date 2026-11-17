# Final frozen-source qualification: independent review

**Verdict: NOT_READY / BLOCKED.** The frozen candidate fails the unchanged
scheduled-to-validated p99 gate: **588.031143 ms > 250 ms**. All 120,000 scheduled
arrivals completed without drops or errors, but those counters do not satisfy
the latency requirement. The failure is independently reproduced from the raw
CSV and cannot be dismissed by historical passing runs.

Review date: 2026-11-17 KST. This review covers source composition, actual CI
checkout and step evidence, retained artifact integrity, and the final gate
decision. It does not claim a new exhaustive audit of all previously qualified
source. No application tests, CI reruns, sustained loads, remote mutations, or
implementation edits were performed during this final evidence review.

## Immutable source attribution

| Role | Commit / tree |
|---|---|
| Original source used for independent RED reproduction | `242f80aea730ee483cc22798fb6450bdb3064081` |
| Published frozen candidate | `89e883fc700ef0250024427a1f09bb88affe0383` |
| Equivalent coordinator checkout | `51ea9dfae53d295c3bc55fcab6db2db78bfeccca` |
| Actual GitHub pull-request merge checkout | `b5c17b6f989bb11575e83544d37cc37fc6385d19` |
| Common complete Git tree of those last three commits | `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55` |
| Main parent, unchanged at observation | `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a` |

The synthetic checkout has the main and frozen candidate as its parents. Its
tree was checked through the GitHub Git Commit API and local Git objects.
Actual job-log checkout output is checked separately; a workflow head SHA alone
does not establish what a job tested.

All **65 changed non-documentation paths** match their accepted author revision,
including file modes, Git blobs, content SHA-256, and expected deletions. The
changed-path union has no extra or missing source, test, CI, or generated-output
paths:

| Accepted correction | Author revision | Paths |
|---|---|---:|
| Registered HTTP evidence parser and regression tests | `6add53b53b57e6fc3826820ac7b6580b493629cb` | 2 |
| Storage lease controller ownership and tests | `e59fc07668e0a282555f2f52dbddf890162254e0` | 3 |
| Queued TypeScript/Dart refresh session ownership | `8ccf8ca7b5a7a973dc209563e2b3256966da89be` | 5 |
| Administrator Jobs/Storage response ownership and rebuilt UI | `b56b74022eced2c767429b435a2abc4031ec37ee` | 55 |

The independent RED/GREEN work for those corrections is retained in
[independent-qa.md](independent-qa.md). It remains valid scoped regression
evidence; it does not override the failed final performance gate.

All **617 original repository evidence files, totaling 15,197,375 bytes**, remain
unchanged in the frozen tree. This comprises 615 files under this workstream and
two files in the older maintainability audit. Identity checks preserve previous
failures and warnings; they do not reinterpret those results as passing.

## Actual full-qualification run

The review is bound to [Full source qualification run 43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611),
attempt 1, triggered by `pull_request` for the frozen source. The expected matrix
contains **15 actual jobs**. Routine parallel workflows are not added to that
count. The final CI metadata and raw decoded logs are retained under
[`evidence/final-master/final-ci/`](../evidence/final-master/final-ci/).

The final live API observation and retained metadata agree: **completed / failure,
14 successful jobs, one failed job, no pending jobs**.

| Actual job ID | Job | Final result |
|---|---|---|
| `133701568842` | Mixed HTTP and sustained fixed-arrival load | **FAIL: p99 gate** |
| `133701569311` | Frontend | PASS |
| `133701569351` | macOS terminal | PASS |
| `133701569373` | Go unit | PASS |
| `133701569385` | Security audit | PASS; raw findings retained |
| `133701569432` | Browsers and recovery | PASS |
| `133701569579` | CodeQL JavaScript/TypeScript | PASS; one raw result |
| `133701569629` | Windows self-update | PASS |
| `133701569656` | Observability artifacts | PASS |
| `133701569733` | CodeQL Go | PASS; 24 raw results |
| `133701569741` | PostgreSQL 16.13 | PASS |
| `133701569796` | PostgreSQL 18.3 | PASS |
| `133701569828` | PostgreSQL 17.9 main race suite | PASS |
| `133701569883` | Release phase artifacts | PASS |
| `133701570002` | PostgreSQL 17.9 `pgbase` race shard | PASS |

An explicit read-only verifier checks the exact 15 job names and IDs, **93
required substantive steps**, workflow/source/attempt binding, actual checkout
in every log, and both compressed and decoded log hashes. It keeps evidence
integrity separate from qualification: intact evidence of a failed gate is not
a successful qualification.

### Completed coverage established from actual logs

- **Frontend:** all 78 test files and **458 tests passed**, including **ApiView
  3/3** in the complete fresh suite. Type checking, browser-example security
  checks, a rebuild from an empty embedded-output directory, and exact committed
  output reproduction passed. The earlier local ApiView timeout remains in its
  original evidence. This full passing job clears that release gate, while the
  cause of the earlier timeout remains unproved.
- **Backend unit and native platforms:** the full unit wrapper ran with
  `PGBASE_FULL_UNIT_RACE=true`; formatting, pinned static analysis, vet,
  skip-policy enforcement, 17 evidence-tool tests, and private attestation
  verification passed. macOS executed the named terminal cancellation/echo
  restoration test with `-race`. Windows passed the five native self-update,
  CLI, durable-file, filestore, and backup packages.
- **Browsers and recovery:** **26 Chromium workflows** and **12 representative
  Firefox/WebKit workflows** passed. The recovery suite used `-count=2
  -shuffle=on`, with **30 top-level and 28 subtest pass executions**. These are
  execution counts, not unique tests. Hardened runtime and Compose shutdown
  checks passed. The expected failed `/bin/sh` entrypoint proves shell absence
  and is not an unexplained workflow error.
- **PostgreSQL:** 16.13, 17.9, and 18.3 completed their configured full integration
  suites; 17.9 uses race detection across its main and separate `pgbase` shards.
  Each non-shard job passed **142 Dart tests**, **123 TypeScript tests**, **five
  top-level live SDK HTTP contracts**, and **two browser SDK example tests**.
  TypeScript reports zero failures, cancellations, and skips. Dart analysis,
  pinned S3, matching PostgreSQL client tools, exact SDK distribution
  reproduction, and benchmark capture passed. PostgreSQL 17.9 also explicitly
  passed `TestMultiProcessBoundedFailureAcceptance` and
  `TestAdministrationSDKsAgainstRealHTTPServer`.
- **Observability and release phases:** dashboard JSON, PromQL, alert rules,
  published metric contracts, immutable phase builds, and the actual process
  rollout with `-race` passed. The eight phase executables intentionally belong
  to their declared historical source commits; they are not all binaries of the
  frozen candidate.

The retained Go wrapper output establishes **49 passing unit packages under
`-race`**, **51 integration packages on each of PostgreSQL 16.13 and 18.3**, and
**50 main integration packages plus one `pgbase` package under `-race` on 17.9**.
No-test packages are counted separately (unit 14; main integration jobs 12 each).
These are package outcomes, not per-test-case totals. The JSON event stream was
consumed by the skip-policy checker and is not separately retained in these job
logs; no individual test total or zero-skip claim is inferred from package lines.

Skipped release-image binding steps are conditional on a digest-pinned release
candidate, which this source run does not supply. The `pgbase` shard deliberately
skips duplicate SDK, benchmark, and non-shard durability work. The final verifier
requires those substantive steps in the appropriate other jobs and does not
treat a skipped gate as a pass. The completed verifier confirms **all 15 actual
checkouts and retained log hashes**, with **92 of 93 required substantive steps
successful**. The sole failed required step is the sustained-load qualification.
Evidence integrity is PASS; qualification is NOT_READY/BLOCKED.

## Independently confirmed failed sustained-load gate

The retained mixed-load ZIP has SHA-256
`560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393`,
matching the GitHub artifact digest and the upload log. Its environment file
begins with the actual `b5c17b6f989bb11575e83544d37cc37fc6385d19` checkout.

The raw CSV has exactly **120,000 ordered arrivals at 5 ms spacing**: 200 RPS for
600 seconds. There are 12,000 each of create/update/delete/get and 72,000 page
requests. All raw error fields are empty; status counts are 12,000 HTTP 201,
96,000 HTTP 200, and 12,000 HTTP 204. The logged intended, enqueued, attempted,
and completed counters are all 120,000; dropped, unscheduled, and errors are 0.

Using the unchanged harness order statistic,
`sorted_ns[(n * percentile + 99) // 100 - 1]`, the independently recomputed values are:

| Metric | p50 (ms) | p95 (ms) | p99 (ms) |
|---|---:|---:|---:|
| Scheduled arrival to validated response | 6.264600 | 319.624000 | **588.031143** |
| HTTP service | 5.854931 | 152.715202 | 277.767335 |
| Generator and queue | 0.254344 | 157.578606 | 426.305938 |
| Generator | 0.221794 | 0.906199 | 1.065102 |
| Worker queue | 0.003535 | 157.205185 | 425.904449 |

Every recomputed quantile matches its log value. **7,726 raw arrivals exceeded
250 ms.** The failure is therefore not a missing-row, wrong-percentile, or
summary-parser discrepancy. The source threshold remains 250 ms, and the
workflow rate/duration remains 200 RPS/600 seconds.

These data establish failure and characterize where elapsed time accumulated;
they do not, by themselves, prove the cause of the change. A historical pass,
unchanged harness, or a hypothesis about host noise cannot convert this result
to PASS. No same-source rerun or threshold adjustment was performed by this
reviewer. A concrete explanation and an independently assessed correction,
followed by a qualifying result under the required gate, remain necessary for
readiness.

The performance review's bounded comparison finds higher WAL synchronization
time and more sampled WAL/pool waiting on a different hosted CPU model. This is
an association, not a controlled causal result. Its worker-queue observations
agree with the independently recomputed series above. The test source performs
post-drain SQL row/create/update and owner/identity/pagination checks before the
p99 assertion; the complete log reports no failure from those checks and reports
only the later latency assertion. The entire Go test nevertheless fails.

## Security and artifact interpretation

Five retained security ZIPs independently match their GitHub sizes and SHA-256
digests. Both CodeQL summaries identify this run and actual synthetic checkout;
their SARIF bytes match summary hashes. Go reports 24 raw results and
JavaScript/TypeScript reports 1. The summaries record non-incremental,
non-diff-restricted analysis of their configured scope (485 Go and 331
JavaScript/TypeScript extracted files). Successful scans do not mean zero raw
findings.

The [fresh security review](final-security-review.md) retains **25 raw source
findings (1 CRITICAL, 17 HIGH, 7 MEDIUM)** and classifies the supplied concrete
paths as `not_actionable`, with source-path counterevidence. The image has **32
raw results (21 MEDIUM, 9 LOW, 2 UNKNOWN)**; the HIGH/CRITICAL gate is empty.
**30 image inputs remain `needs_review`** and two receive architecture exclusions
limited to the captured amd64 image. These dispositions do not represent accepted
risk declarations or scanner suppressions. UNKNOWN findings remain unresolved.

This independent release review cross-checks the report's artifact identity,
counts, 57 input mappings, source binding, referenced-file identities, and stated
scope. It does not claim a second exhaustive security audit. CodeQL's configured
Go/JavaScript/TypeScript scope excludes platform-inactive Go files and does not
analyze Dart or SQL as languages; passing native platform/runtime tests do not
erase that static-analysis boundary.

The downloaded 51,295,460-byte release ZIP independently matches the API digest
`0a2562910ca4cac764148f9cb69ab8e0d49874b2a9fa54f2d47205ea37bceb6e`.
All eight executable member hashes match the two raw phase manifests. The
retained manifests exactly match the archive members. The large rebuilt
executable archive remains subject to hosted Actions expiry; the repository
retains its metadata, original small manifests, and member digests. This is not
a claim that the full executable archive is durably stored in Git. Its recorded
hosted expiry is `2027-02-14T23:47:34Z` (2027-02-15 08:47:34 KST). Across the full
run, all ten artifact archives have verified API hashes: nine original ZIPs are
retained in Git and this larger executable ZIP has the explicitly narrower
retention described here.

Job logs are the connector-decoded response re-encoded as UTF-8, then losslessly
gzipped. Both retained forms are checksummed. These checks do not claim byte
identity to an original HTTP log ZIP representation.

## Decision boundary and evidence

The accepted functional corrections and passing surrounding gates are retained
as a useful checkpoint. The frozen candidate is **not merge-ready** while its
required performance gate fails. PR #148 remains Open, Draft, Unmerged, with
auto-merge disabled at the recorded observation; this review authorizes no merge
or deployment.

Subsequent report/evidence commits have different commit and tree IDs. They may
preserve runtime bytes, but must not be described as independently runtime-tested
by this run without an explicit source-equivalence statement.

Independent evidence is under
[`evidence/final-master/independent-final-ci/`](../evidence/final-master/independent-final-ci/):

- `source-composition-complete.json`: all 65 accepted path identities, tree
  equality, and all 617 historical evidence identities.
- `frontend-verification.json` and `coverage-observations.json`: actual coverage
  anchors, native/platform results, and conditional scope.
- `go-suite-package-counts.json`: exact suite-step package counts, with their
  limits distinguished from individual test counts.
- `initial-artifact-integrity.json`: independently opened security archives and
  release archive/member checks.
- `benchmark-artifact-integrity.json`: the three final PostgreSQL benchmark ZIP
  hashes, which complete the ten-artifact integrity inventory.
- `failed-load-gate-verification.json`: raw arrival, status, percentile,
  threshold, environment, and artifact checks establishing the failure.
- `verify_retained_ci.py`: reproducible read-only job/step/checkout/log verifier.
- `final-live-state.json`, `coordinator-metadata-crosscheck.json`, and
  `completed-ci-verification.json`: final direct API observation, agreement with
  retained coordinator metadata, all 15 actual checkouts, 93 required step
  outcomes, and the expected blocked qualification result.
- `security-raw-crosscheck.json` and `reviewer-crosschecks.json`: raw security
  counts and the bounded security/performance reviewer handoff checks.
- `manifest.json`: final hashes for this report and its independent evidence.
