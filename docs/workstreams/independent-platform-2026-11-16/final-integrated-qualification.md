# PgBase — final integrated qualification

## Final decision

- **Functional / Non-performance = Ready**
- **Performance = Pass**
- **Overall = `READY_FOR_MANUAL_MERGE`**

The final fixed-source [qualification run 43861850923](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923) completed **15/15 successful jobs**: all **14 mandatory functional jobs**, plus the single unchanged 600-second performance gate. There are no failed, cancelled or unfinished jobs in this run. The performance author and independent reviewer separately recomputed all 120,000 arrival records from the original archive and obtained identical passing results.

This decision is a technical recommendation for the user's manual merge decision; the PR remains **Open / Draft / Unmerged**. No merge, production access, deployment, release/tag or package publication was performed. The earlier failed runs remain historical evidence with their original source scopes.

## Final closeout audit — 2026-11-16

This closeout recovered the prior session's unpublished documentation/evidence tree
`bef5cdeee47470dd488d4fdb6da31cbc2cd31754` (local commit
`28e4f1b0a33484c1a7997ca75913a52ff441b59c`) and checked it against live GitHub
results before publication. The PR still pointed to the tested candidate on entry;
no duplicate final report or final handoff comment had been published.

Two new independent evidence reviewers were created, with maximum concurrency two.
Agent A performed **41/41 passing offline checks** of the freshly downloaded
performance archive. Agent B independently compared the live 15-job API/logs,
Git source trees, workflow conditions, retained security artifacts and previous
Listener/functional QA evidence. Their reviews are evidence verification, not new
runtime test executions. The prior execution session's three reviewers and focused
race/RED/GREEN tests are recorded separately below.

- [Current independent performance review](evidence/final-integrated/closeout-20261116/performance/independent-performance-review.md)
  and [41-check result](evidence/final-integrated/closeout-20261116/performance/performance-independent-verification.json).
- [Current source/CI/security review](evidence/final-integrated/closeout-20261116/source-ci/source-ci-verification.json)
  and its adjacent source, job, security and log-integrity ledgers.
- [Closeout scope and identities](evidence/final-integrated/closeout-20261116/scope.json)
  and [preservation/publication check](evidence/final-integrated/closeout-20261116/documentation-identity.json).

No benchmark, CI workflow, Go test, Firefox reproduction or security scan was
launched in this closeout. All existing evidence paths in the tested source are
preserved byte-for-byte. The prior unpublished final decision ledger is retained
under the new closeout directory rather than overwriting the original ledger.
The final documentation commit and its full tree are recorded in PR #148's
immutable handoff links. That documentation tree differs from the tested full tree
only in documentation/evidence; runtime, tests and workflows are identical.

## Exact source identity

| Identity | SHA / tree |
| --- | --- |
| Starting PR HEAD | `23b6c4090e453587325d9dfad8fec6d854f3bee2` |
| Tested PR runtime and workflow HEAD | `4091c3be20ec8db177886a2a85550924fce37ff5` |
| Actual CI synthetic merge checkout | `ba838872d72993abf1953dcafc64ba043f978c06` |
| Exact Git tree shared by candidate and CI checkout | `653a46758f9e6af9662b77ecfe2f069a2ac94cb4` |
| Local candidate reconstructed by Git-data publication | `51729da256db425872ba47591017aa0dc9633c4b` — same tree |
| Unchanged main/base | `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a` |
| Independent focused GREEN runtime/CI source | Published `277c18cbb5333ca2a925ccb84628702c29b7fcd6`, local `ff5cd9ac3188923400d26297280faa87839b590d` |
| Focused GREEN common tree | `1d30798382078731b9442e4a42aadb0a9d328a34` |

All 14 functional logs and the performance environment identify the actual synthetic checkout above. The candidate and synthetic tree were checked through Git, independently of the PR metadata. Evidence-only additions between focused GREEN and the final run did not change runtime, tests or workflow content. All required full gates subsequently executed on the final tree.

The post-run handoff commits add documentation and evidence only. They are **not** presented as another test execution or a new runtime qualification. The final documentation HEAD is linked from PR #148; its own commit ID cannot be embedded in the same immutable commit. [Publication mapping](evidence/final-integrated/coordinator/publication-map.json) records local/published commit pairs and exact tree checks; [source verification](evidence/final-integrated/functional/source-verification.json) and [independent source verification](evidence/final-integrated/independent/qualification-source.json) retain the execution identities.

## Bounded changes actually made

### Request-pool capacity and schema listener

[Runtime correction 277c18cb](https://github.com/yakisoba0728/PgBase/commit/277c18cbb5333ca2a925ccb84628702c29b7fcd6) moves the long-lived schema listener onto a dedicated initialized connection, using the existing pgx connection handoff. The old configuration intentionally included the listener in the pool4 budget: with the listener running, only three request connections could be acquired concurrently. The regression failed on both initial connection and reconnect against the unchanged old listener.

The corrected source makes all four request connections available while retaining session security, the shared backup writer advisory lock, reconnect, PID cleanup and bounded shutdown. This is the current user's explicitly authorized separation of listener capacity from request capacity. It is **not** a newly demonstrated connection leak and does not prove the sole cause of the historical 547 dropped arrivals.

The new test and deployment budget document the behavior. A read-only diagnostic addition captures `full_page_writes`; no PostgreSQL setting, workload, queue policy, latency formula, error accounting, timeout or acceptance threshold was weakened.

Author verification: focused PG17.9 race **30 top-level +7 subtests**, zero failures/skips, plus vet. Independent verification restored the old listener and reproduced the failure, then passed the integrated source under race: **25 top-level +17 subtests**, zero failures/skips. These are different selected commands, not duplicate counts of one execution. Exact commands and raw results are retained under [performance](evidence/final-integrated/performance/) and [independent QA](evidence/final-integrated/independent/).

### Complete final browser qualification

[CI correction cb7247f4](https://github.com/yakisoba0728/PgBase/commit/cb7247f47c52bf8b6a8160b785d46ad4f486db06) changes one reusable-workflow target in `full-qualification.yml` to the existing `nonperformance-e2e.yml`. It retains Chromium, hardened runtime and black-box recovery and adds the already-required Firefox/WebKit coverage and failure-trace retention.

The independent reviewer compared both callable workflows and confirmed unchanged required assertions, fixture isolation, timeout, retry and strict console behavior. No browser test was skipped or filter relaxed.

The previously accepted Auth/embedded/batch, Jobs occurrence and TS/Dart iterator fixes remain intact. This continuation did not reimplement them, change schema69, redesign the UI, remove license notices or restart a repository-wide audit.

## Final integrated execution — all 15 jobs

| Required job | Job ID | Result |
| --- | --- | --- |
| [backend / Release phase artifacts](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280386955) | 133280386955 | PASS |
| [backend / Windows self-update](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387001) | 133280387001 | PASS |
| [backend / macOS terminal](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387081) | 133280387081 | PASS |
| [security / audit](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387112) | 133280387112 | PASS |
| [frontend / test](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387146) | 133280387146 | PASS |
| [source-analysis / analyze (go, manual)](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387167) | 133280387167 | PASS |
| [browser-and-recovery / e2e](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387178) | 133280387178 | PASS |
| [source-analysis / analyze (javascript-typescript, none)](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387183) | 133280387183 | PASS |
| [backend / Observability artifacts](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387248) | 133280387248 | PASS |
| [backend / PostgreSQL integration / 16.13](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387312) | 133280387312 | PASS |
| [backend / PostgreSQL integration / 18.3](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387317) | 133280387317 | PASS |
| [backend / PostgreSQL integration / 17.9 / pgbase](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387341) | 133280387341 | PASS |
| [backend / PostgreSQL integration / 17.9](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280387368) | 133280387368 | PASS |
| [backend / Go unit](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280388096) | 133280388096 | PASS |
| [mixed-http — including the 600-second open-loop gate](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280386571) | 133280386571 | PASS |

Observed test evidence:

- Go unit/static: formatting, pinned lint, vet, race and private attestation integration passed; **49 package pass summaries**. The logger suppresses individual successful events, so no invented total unit-test count is supplied.
- PostgreSQL16.13 and18.3: **51 integration package pass summaries each**. PostgreSQL17.9: **50 core package summaries plus the separate application package**, both under race. Existing schema69, Storage, isolated MinIO, backup/restore, Auth, Jobs, Realtime and multiprocess gates completed.
- Each of the three core PostgreSQL matrix jobs passed TypeScript **119/119**, Dart **138/138** with analysis, actual server/administrator SDK contracts and distribution reproducibility. SDK HTTP conformance reported five top-level and four subtest executions per core job.
- Administrator UI: **78 files /449 tests**, type checking and clean embedded build equivalence; four browser-security example tests also passed.
- Browser E2E: **Chromium26, Firefox6, WebKit6**. Hardened nonroot/no-shell runtime passed. Black-box recovery executed **15 distinct scenarios twice:30 top-level executions plus28 subtests**, with shuffle preserved.
- Windows, macOS, observability, release-phase artifact validation, source analysis and supply-chain/image gates passed. Artifact validation does not constitute a release or deployment.
- No named test failures or skips were found in the retained functional logs. Required-step success was independently compared with the live job API.

The normal synchronized Backend43861764294, Frontend43861764291 and Security43861764157 runs also completed successfully on the same source; they are supplemental evidence. Skipped nonperformance and repeated-performance wrapper runs were deliberately not used as passes. The full run contains all required gates; there was no repeated ABBA campaign.

[Functional report](evidence/final-integrated/functional/functional-report.md), [result and step ledger](evidence/final-integrated/functional/functional-result.json), [all jobs](evidence/final-integrated/functional/jobs.json) and [raw-log manifest](evidence/final-integrated/functional/raw-log-manifest.json) preserve exact commands, test counts, links and hashes. The 14 complete logs are retained losslessly in Git as deterministic gzip: **4,605,259 original bytes /666,670 compressed bytes**, with both original and compressed SHA-256.

## Final performance execution

The only final 600-second execution was [mixed-http job133280386571](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280386571). No second final attempt was required.

| Criterion / measurement | Actual result |
| --- | --- |
| Input |200 RPS x600 seconds,120,000 fixed scheduled arrivals,5ms spacing |
| Scheduled / attempted / completed |120,000 /120,000 /120,000 |
| Dropped / unexpected errors / unscheduled / incomplete |0 /0 /0 /0 |
| Scheduled arrival to response validation p50 |7.400337ms |
| Same end-to-end p95 |12.632966ms |
| Same end-to-end p99 |**22.221162ms**, limit250ms |
| Maximum measured end-to-end latency |126.553471ms |
| Request-pool maximum |**4**, unchanged |
| Durable DB/owner/CRUD/ETag/relations/cursor assertions |PASS |
| fsync / synchronous_commit / full_page_writes |ON before and after |
| Final verdict |**Pass** |

The job used PostgreSQL17.9, Go1.26.11, Linux/amd64 Ubuntu24.04-based Azure runner, four logical CPUs on Intel Xeon Platinum8573C, and16,759,304,192 bytes of host RAM. Exact host, container, database settings and measurements are preserved with the archive. The scheduled load interval was600 seconds; measured interval600.005995104 seconds. The749.45-second Go test wall time includes fixtures and validation and is not substituted for the scheduled-load interval.

Connection accounting is explicit: **4 pooled request clients +1 dedicated schema listener +1 existing backup-gate client +1 existing diagnostic observer =7 client sessions**, versus6 historically. The diagnostic observer is optional and is not assumed to be continuously present in production. The [deployment guide](../../deployment.md) records this conditional seven-client budget. The sampler excludes its own observer, normally showing6 clients.150 of6,000 snapshots include an additional autovacuum backend; that is a background worker, not an eighth client or a fifth request-pool connection. Sampled database-activity backends can reach 8 including the observer; the sampler filters the current database and does not enumerate every cluster or operating-system process.

The author and independent reviewer used separate parsers and obtained exactly the same counts and percentiles. All120,000 indices, intended schedule and arrival accounting were checked. No fictitious latency was assigned to a dropped arrival. Percentiles for different timing stages were not added together.

See [final performance qualification](final-performance-qualification.md) for the complete protocol, workload/environment, raw/derived evidence, durability and connection details, and interpretation limits. [Independent recomputation](evidence/final-integrated/independent/final-performance-recheck.json) is preserved separately.

## Previous execution session: independent QA and agent accounting

In the preceding execution session, three actual child agents were created and all three completed: A functional CI, B performance/PostgreSQL and C independent QA. Maximum concurrent child agents: **3**, or4 including the coordinator. Each used a separate worktree with assigned file ownership. CI jobs are not counted as agents.

C authored no product correction. It independently reviewed A/B changes, reproduced the original listener failure, exercised focused race/lifecycle/backup behavior, checked the fixed-source workflow/tree, matched all14 functional logs and required steps, recomputed the performance CSV before seeing B's numbers, and reconciled fresh security artifacts. The final verdict is **ACCEPT_FINAL_INTEGRATED_QUALIFICATION**; the [independent review](evidence/final-integrated/independent/final-review.md) records its evidence and scope. No child agent remains active.

## Fresh security evidence and retained limitations

Fresh whole-configured-source CodeQL extraction and successful invocation were verified for the exact candidate. Changed runtime paths were included. **Go24 and JavaScript1 findings remain**, with the same rule/message/fingerprint identities and primary locations as the preserved historical baseline; all sources referenced in those reported dataflows retain their original Git blobs. Successful CodeQL execution is not a zero-vulnerability claim. The unchanged raw SARIF security-severity scores classify the 25 findings as **1 CRITICAL, 17 HIGH and 7 MEDIUM** (24 Go + 1 JavaScript); these are scanner scores, not newly validated vulnerabilities. Historical per-location dispositions in `final-security.md` and `phase-nonperformance-security-ci.md` remain in force. Raw SARIF summaries retain `unreviewed`; the retained human dispositions are a separate evidence layer. The closeout comparison found no new finding identities or changed source-flow blobs. The zero HIGH/CRITICAL count below applies only to the image scan, not CodeQL.

The image report contains the same **32 records:21MEDIUM,9LOW,2UNKNOWN; HIGH/CRITICAL0**. Raw image, SBOM and runtime manifests were compared, including148 unique CA fingerprints. Native-helper/UNKNOWN classification, upstream distribution and architecture limitations remain recorded. The narrow comparison does not replace a new repository-wide finding-by-finding audit.

The historical Firefox font-abort cause remains unproved. It did not recur in this final mandatory run; no unsupported claim of causal repair is made, and a historical non-reproduced event is not used to turn today's passing gate into a failure.

Existing operational boundaries remain: ambiguous Storage writes retain conservative cleanup tombstones; source-storage migration still requires writer quiescence; external Jobs effects require handler-level idempotency/fencing. Real external OAuth/OIDC/SMTP provider acceptance and broader device/accessibility combinations remain outside this bounded qualification. These limitations are preserved, not silently declared newly tested.

This single disposable CI environment establishes the specified workload gate. It does not establish universal production capacity or prove that moving the listener was the sole explanation of the historical547-drop failure. That failed run and its raw evidence remain valid historical observations.

## Raw artifacts, links and integrity

The original performance Actions artifact expires **2026-11-30 21:08:39 KST (12:08:39 UTC)**. Its original bytes are also retained in Git. The raw CSV SHA-256 is `2bbcfdef50e9c35e68c9083fb75d7893b1db006e12724b9e2233f7219b3bddb7`.

The original performance ZIP is3,528,654 bytes and has SHA-256 `82f0397e6b0ab5e1d71c96ebc2c4af35137fc8638d60c3c262b8208d5935d84e`. It is retained in Git as well as [Actions artifact14224442513](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224442513). The five security ZIPs and all14 functional logs are also retained in Git, so the core qualification evidence does not depend solely on temporary Actions retention.

| Original Actions artifact | Bytes | SHA-256 | Integrity provenance |
| --- | --- | --- | --- |
| [pgbase-sbom](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224830401) | 115338 | `ed6a0eff2a84346d6282bbdae500402e0b3cfc9bae243487297036d84d7392f2` | Locally recomputed |
| [benchmark-postgresql-16.13](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224637685) | 2758 | `f8907e11d99a57ad881dec7ae9a6904bf08c1afc2b08bef9cfad6810601056f8` | GitHub-reported |
| [mixed-http-ba838872d72993abf1953dcafc64ba043f978c06](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224442513) | 3528654 | `82f0397e6b0ab5e1d71c96ebc2c4af35137fc8638d60c3c262b8208d5935d84e` | Locally recomputed |
| [source-security-javascript-typescript-ba838872d72993abf1953dcafc64ba043f978c06](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224420779) | 122745 | `cde3658427f0f01d5c4cd4347d90701d6a6c496c2ee97647957fc2dc0a3849c7` | Locally recomputed |
| [benchmark-postgresql-18.3](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14224162512) | 2742 | `5a1f8613edbd4f173ff78a296d968e01d863013231ac311dde920f2758dafc3c` | GitHub-reported |
| [release-phase-artifacts](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14223885964) | 51294341 | `3a7c5df6be0acc18573afe8d84f212421ecc2ff8c48f92751083dcffd2a83605` | GitHub-reported |
| [benchmark-postgresql-17.9](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14223878730) | 2736 | `8e6b773b5348c5a9fad78d169322cebe023c55b3b77ba96633b8ecf48753dca9` | GitHub-reported |
| [source-security-go-ba838872d72993abf1953dcafc64ba043f978c06](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14223373289) | 92859 | `e2a386fd8e7d1ab2902ac0c9ca14e5812dba6c4d2a4a869ff651207b0fcdc331` | Locally recomputed |
| [runtime-assets-ba838872d72993abf1953dcafc64ba043f978c06](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14223078436) | 206512 | `be44bda6fd90f80903dfc53e13fff4a06a8eb7f6a37f008690c8b2d866247e01` | Locally recomputed |
| [pgbase-image-scan-sarif](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/14223058511) | 15494 | `40425e3fe5aeed669fb0d0266077fc397224e8ab2487fe5f7356e46e23537c4e` | Locally recomputed |

[Complete artifact index](evidence/final-integrated/coordinator/artifact-index.json) retains API timestamps/expiry and distinguishes locally recomputed digests from GitHub-reported ones. The51MB release-phase archive and the existing small benchmark artifacts remain linked rather than duplicated into Git; their API digests are not mislabeled as independent local recomputations. [Security checks](evidence/final-integrated/independent/fresh-security-recheck.json), [functional log hashes](evidence/final-integrated/functional/raw-log-manifest.json) and the performance report provide per-file integrity evidence.

## Handoff and stop

The two required axes have passed on the fixed runtime. No further performance run, code change, broad investigation or automatic merge is needed in this round. The remaining action is a human review of Draft PR#148 and a manual merge decision.

Main remains unchanged. The final report/checkpoint publication is documentation/evidence only and uses CI suppression so it cannot consume another600-second run. The [continuation state](continuation-state.md), this report, the performance report and the PR description identify the tested source separately from the final evidence HEAD.
