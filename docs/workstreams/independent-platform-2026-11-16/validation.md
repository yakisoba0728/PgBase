# Continuation: implementation and verification

This is the current evidence record for Draft PR [#148](https://github.com/yakisoba0728/PgBase/pull/148). The [historical handoff](validation-handoff-history.md) is retained separately. This continuation used actual local tools and three independent agents; previous statements that those facilities were unavailable do not describe this work.

No main push/merge, production database/server access, data reset outside disposable fixtures, deployment, tag, package publication or release promotion was performed. Schema remains **67**. A completed bounded test is not certification for arbitrary production workloads.

## Revision boundaries

- Main baseline: `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`.
- Previous handoff: `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b`.
- Original CodeQL source: `cf50eed36e7d2f15915ade9f66a156faf34871ab`; its relevant runtime files equal the handoff. The actual original artifacts contain **27 Go and 3 JavaScript/TypeScript findings**.
- First combined continuation runtime: `7619dbf790c9ccf88079247482b5db18fe8f018a`, tree `4c0ba65fe43f81ef0e5c9a2700af88009ef4a156`.
- Its CI synthetic merge: `e719d9d31655d4e6e4724bbb1dda5007235bc7e5`, with the same tree. This is a PR test merge, not a merge into main.
- Final runtime candidate: `5e4b71d1f602c9c136809bf65f2375bf75fc4a9f`, tree `4599bc705d4c4ec440e273cc6db3397f3edcc11d`; PR merge `2d712ba6239f2d19f1329400848173842ed7874c` has the identical tree. Qualification is recorded below after the full-scope CodeQL and public runtime-data corrections. Later evidence-only commits must not be described as a full runtime rerun.

GitHub connector publication creates new commit identities from the local logical commits; every remote tree was compared to the local expected tree before advancing the branch with an expected-head lease. [The mapping](evidence/runtime-commit-map.json) records that equivalence. Original/other worktrees were preserved; no force push was used. Measurement commits are identified separately in [performance.md](performance.md), because identical query source does not mean an identical whole repository revision.

## First combined runtime: completed CI

[Full source qualification run 43777701630](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630) completed all **15 jobs** successfully on the first combined runtime. Its source-analysis jobs completed but were **diff-informed**, so their 0 Go / 1 JavaScript results are expressly rejected as whole-source qualification. The next candidate disables that mode and requires evidence of full scope. This limitation does not invalidate separately executed runtime tests.

| Check | Actual job | Observed result |
| --- | --- | --- |
| Go unit, race, static/repository policy | [133007664114](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664114) | Full unit race, pinned static analysis, vet, skip policy and private-attestation disposable fixture passed |
| PostgreSQL 16.13 | [133007664032](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664032) | Integration/skip policy, configured S3 fixture, Dart and TypeScript/live public SDK gates passed |
| PostgreSQL 17.9 core | [133007664129](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664129) | Integration race, bounded multiprocess, SDK and live administrator SDK gates passed |
| PostgreSQL 17.9 application shard | [133007664135](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664135) | Application integration race passed, 276.659s |
| PostgreSQL 18.3 | [133007664153](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664153) | Integration/skip policy and SDK gates passed |
| Administrator frontend | [133007663937](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007663937) | Typecheck, **77 files / 427 tests**, browser example regressions and exact empty-directory dist rebuild passed |
| Chromium and black-box recovery | [133007663894](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007663894) | **25 browser tests passed, one existing managed-backup fixture exclusion**; hardened runtime and black-box crash/readiness/graceful-drain tests passed |
| Mixed HTTP | [133007663607](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007663607) | **4000 requests, zero errors**, workers 1/8/32/4, pool 4, strengthened ordering/filter/owner/relation/cursor checks |
| Image/dependency audit | [133007664047](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664047) | Gate passed; reachable Go and HIGH/CRITICAL image findings zero, but all-severity image inventory contained **37 records**; see security review |
| Windows/macOS | [133007664195](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664195), [133007664142](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664142) | Native Windows self-update and macOS terminal tests passed |
| Artifact/observability tests | [133007664095](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664095), [133007664164](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630/job/133007664164) | Disposable immutable-phase rollout and observability artifact gates passed; no release published |

The PG17 bounded multiprocess log reports two processes, 32 unique requests, 12 repeated idempotency requests (one 201 and eleven 409), 35 durable records, one killed/restarted process, resumed realtime delivery and cross-process revocation. These are bounded fixture results, not an unbounded exactly-once delivery guarantee. The [independent backend log review](evidence/agent-reviews/first-runtime-backend-summary.md) distinguishes package counts, named skips, standalone suites and live contracts.

Browser exclusion: `ui/admin/e2e/authenticated/admin.spec.ts` retains its existing requirement `PGBASE_BROWSER_MANAGED_BACKUPS=1` for the managed backup host fixture. This candidate did not introduce a skip. The new browser example contract ran against the actual served example module. Local Chromium could not create the required socket in the workspace; hosted Chromium CI supplied the actual browser evidence.

Black-box E2E includes two successful application/database abrupt-termination scenarios with committed-state survival, uncommitted rollback and idempotency assertions, plus readiness recovery and two graceful-drain scenarios. The expected error from attempting `/bin/sh` in the distroless image is a successful hardened-runtime check, not an E2E failure.

Routine checks on the same source also passed: [Backend 43777701494](https://github.com/yakisoba0728/PgBase/actions/runs/43777701494), [Frontend 43777701470](https://github.com/yakisoba0728/PgBase/actions/runs/43777701470), [Security 43777701501](https://github.com/yakisoba0728/PgBase/actions/runs/43777701501).

## Final runtime qualification

[Full run 43780150632](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632) tests runtime `5e4b71d1` through the identical-tree PR merge `2d712ba6`. **All 15 jobs completed successfully**, from 00:24:12 to 00:36:44 UTC on 2026-11-16. [The machine-readable ledger](evidence/qualification-final/ci-ledger.json) preserves every job/step conclusion.

| Check | Final job | Observed result |
| --- | --- | --- |
| Go unit/race, static analysis, vet and repository policy | [133015260951](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260951) | Passed; `PGBASE_FULL_UNIT_RACE=true`, 12 parser tests and disposable private-attestation fixture also passed |
| PostgreSQL 16.13 | [133015260615](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260615) | Integration/skip policy, S3, complete standalone SDKs and all four live SDK contracts passed |
| PostgreSQL 17.9 core | [133015260618](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260618) | Integration race, S3, all SDK gates, bounded multiprocess and both administrator SDK consumers passed |
| PostgreSQL 17.9 application shard | [133015260567](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260567) | Application integration race passed, 399.842s |
| PostgreSQL 18.3 | [133015261053](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015261053) | Integration/skip policy, S3, complete standalone SDKs and all four live SDK contracts passed |
| Frontend | [133015260961](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260961) | 427 tests/77 files, typecheck, example security tests and exact empty-dist reproduction passed |
| Chromium / image / recovery | [133015260580](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260580) | 25 browser passes/one existing managed-backup exclusion; hardened image and black-box `-count=2 -shuffle=on` passed |
| Full-scope Go / JavaScript CodeQL | [133015260677](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260677), [133015260937](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260937) | Completed with **24 Go / one JavaScript finding**, no incremental/diff-range restriction; all remaining claims match reviewed originals |
| Image/dependency audit and actual asset capture | [133015260812](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260812) | Passed; actual 148-root bundle, tzdata 2026f and native trust path verified. HIGH/CRITICAL 0; all-severity **32** remains visible |
| Mixed HTTP | [133015260352](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260352) | 4000 requests, zero unexpected errors, all strengthened data-contract assertions passed |
| Windows / macOS | [133015260893](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260893), [133015261029](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015261029) | Native self-update/terminal checks passed |
| Immutable phase / observability artifacts | [133015260938](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260938), [133015260850](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632/job/133015260850) | Passed against disposable fixtures; no release publication |

The final browser run collected 26 tests: 25 passed in 53.4s and the same managed-backup fixture test was excluded. Both abrupt application/database termination scenarios passed (13.52s/13.51s), with readiness recovery and graceful draining also repeated. All four generated E2E credential environment keys appeared 11 times each in subsequent logs, always as `***`; root and the frontend agent independently checked that no unmasked assignment remained.

The live-SDK coverage defect in the preceding run is now corrected: `-run '^TestSDK' -v` selects public records, relation/file regression, text record identity and Realtime (both TypeScript and Dart). These are separate from the standalone 109 TypeScript/130 Dart suites. PG16, PG17 and PG18 logs explicitly show all four and both Realtime subtests passing; their live-test package times were 21.975s, 22.999s and 24.951s respectively. PG17 also repeats the bounded 32-unique/12-same-key/35-durable-record fixture and both actual-process administrator SDK consumers. Those administrator backup checks are not a destructive restore test.

The [independent final runtime review](evidence/qualification-final/qualification-final-runtime-review.md) confirms 51/50/51 integration package passes on PG16/17-core/18, 12 no-test-file packages per job and zero observed named skips. These are package counts, not individual Go case totals. The four reviewed jobs checked out the same PR merge.

All six final artifact ZIPs are retained unchanged in [qualification-final](evidence/qualification-final/README.md), with Actions artifact IDs and independently checked SHA-256 values. CodeQL loaded 37/105 queries, extracted 483/327 files and reported 35/103 rule descriptors. Go's 24 results have 14 error / 10 warning levels; JavaScript's one is a warning. These levels are scanner metadata, not the reviewed exploit severity. The remaining 25 claims match the original narrow false-positive decisions; original Go 1/10/17 and JS 2/3 are no longer reported. No result was deleted or suppressed to obtain this count.

The final image is `sha256:2966e2bfca8988c3d3c87808f19ae2869279a32de07d08bd4a086f0a7106e8ba`. Actual certificate bytes, both removed root fingerprints' absence, public trust environment, tzdata source and package status were inspected. The 32 remaining image records comprise 21 MEDIUM, nine LOW and two UNKNOWN; all concern native libc/OpenSSL dependencies. They remain classified with explicit limits and upstream-versus-distribution conflicts in [security-review.md](security-review.md).

Routine checks on the final runtime also passed: [Backend 43780150689](https://github.com/yakisoba0728/PgBase/actions/runs/43780150689), [Frontend 43780150543](https://github.com/yakisoba0728/PgBase/actions/runs/43780150543) and [Security 43780150535](https://github.com/yakisoba0728/PgBase/actions/runs/43780150535).

## Local implementation and regression evidence

The local toolchain was Go **1.26.11**, Dart **3.13.3**, Node **24.22.1**, and a private PostgreSQL **17.9** build including `pg_trgm`. Database startup, test execution and shutdown occurred within one execution lifetime; no operational endpoint was used. A shared workspace network/process limitation required this lifecycle, rather than assuming background servers survived between calls.

| Boundary | Reproduction / result |
| --- | --- |
| Original SARIF reporting | Actual ZIP SHA-256 and job/source identities verified. Extension descriptors restored missing severity metadata. Conflicting index, incomplete analysis and diff-only qualification cases fail closed. Final parser suite **12/12**. |
| Archive expanded-size arithmetic | Tiny archive with a small entry followed by `MaxInt64` header failed the old expected early-limit rejection (`unexpected EOF`); fixed subtraction check rejects before reading, with staging cleanup. |
| TOTP | Deterministic `070624` and `+70624` were both accepted before. Regression now accepts the six-digit form and rejects signed/non-digit aliases. No full MFA bypass was claimed. |
| Mail | Quote/markup-bearing HTTPS action URL reproduced extra nodes/broken links before contextual rendering. Tests now cover dynamic contexts, invalid template save/execute, SMTP CR/LF rejection and DATA dot stuffing. |
| Security database integration | Affected authentication/backup packages: **237 top-level passes**, 409 passing events with subtests, zero failures. Existing unconfigured-S3 fixture exclusions: four top-level tests/six events. The separate CI S3 fixture covers those paths. |
| Example URL/SSE lifetime | Four actual-script Node tests failed before and passed after; hosted browser contract additionally exercises the example. |
| Administrator SDK lifetime / cancellation | Initial TypeScript focused set had 11 failures, then 37/37 passed; seven new Dart administrator lifecycle tests failed before and passed after. Additional cancellation compatibility test passes with `AbortSignal.any` absent. |
| Public bearer SDK lifetime | New stale response/error/file-preparation tests failed **15/15 TypeScript** and **18/18 Dart** before; all pass after. Shared store changes cannot silently publish old data or dispatch delayed file preparation with a replacement bearer. |
| Final SDK adoption/continuation | Independent review reproduced simultaneous TypeScript auth completions, record/upload outer continuations and Dart delayed storage. New **15 TypeScript / 20 Dart** tests pass after the follow-up fixes; original independent repros now reject `session_changed`. |
| Complete live SDK selection | All four existing tagged contracts passed locally on PG17.9 before widening CI. The first full CI had selected only the public-record fixture; final matrix results are recorded above. |
| Complete SDK suites | **109/109 TypeScript**, **130/130 Dart**, Dart analysis clean, legacy/type checks and empty-dist exact reproduction passed. Root repeated focused final lifetime/adoption suites; security independently repeated the adoption suites. |
| Administrator UI | Five actual DOM text-boundary regressions; complete **427 tests in 77 files**, typecheck and clean-dist reproduction passed. Fixed the UsersView fixture's actual pagination-reset/observer lifecycle; no deadline increase or added skip. |
| Query allocation | Ordered/unsorted ownership tests, scalar/hydrated byte handling, full-width cursor and over-budget input tests passed. Hydration injects NaN to prove final serialization is not bypassed. Existing assembler budget tests remain. |
| Realtime | **21 existing top-level race tests** passed. Component fan-out benchmark covers 1/32/256 queues with one eighth prefilled slow consumers; no network capacity claim. |
| Dependency update | x/crypto 0.58→0.59 selected no other dependency version. Module checksums and official tag verified; five affected unit race packages, two mock-ACME integration races and all three service-auth integration tests passed. |
| CI changes | Pinned actionlint, shell syntax, repository diff checks, parser regressions and disposable-secret masking smoke passed. Final image/build behavior requires the actual CI evidence above. |

Raw/per-test agent records are retained in [evidence/agent-reviews](evidence/agent-reviews). Source-to-published mappings avoid presenting unpushed agent commit identities as GitHub commits. [Performance evidence](performance.md) separately records exact baseline/candidate source hashes and measurement harness revisions.

## Independent review and integration

| Actual agent | Owned work | Cross-review and corrections |
| --- | --- | --- |
| Security | 18 original Go findings, backup/TOTP/mail fixes, runtime public trust/timezone data | Reviewed performance and SDK code. Found the final SDK adoption/persistence races and a mixed-page assertion gap; these were fixed before final qualification. |
| Backend / Performance | Nine original Go allocation/cursor findings, query allocation changes, native/mixed profiling, x/crypto provenance | Reviewed security patches and runtime-data preparation; provided exact before/after evidence and retained adverse latency results. |
| Frontend / SDK | Three original JavaScript findings, example/UI regressions, TS/Dart operation scopes, generated artifacts | Reproduced security review's additional races, repaired them with failing-before tests, and reviewed migration language against actual SDK behavior. |
| Root | Recovered original artifacts/logs, source/CI scope, integration, shared-boundary review, realtime fixture, publication and documentation | Verified tree equality and remote branch leases; repeated focused security/SDK tests, reviewed all performance hashes and real CI logs. Identified diff-informed qualification and final-image inventory gaps. |

Separate worktrees prevented ownership collisions. The first SDK patch was not accepted merely because its original tests passed: independent review added concrete scheduling counterexamples. Performance review likewise strengthened correctness assertions without rewriting the original paired timing data.

## Performance and remaining scope

The paired 500000-row profile contains **24000 measured native queries** across baseline and candidate, zero errors. A 50-row page fell from **1,116,841 to 517,981 bytes/op** (-53.6%). The mixed HTTP pair contains **8000 requests**, zero errors and 30% writes; it verified status, owner isolation, relation values, cursor continuation and page non-overlap. It exercised filter/sort queries but explicit rank/title/per-page uniqueness assertions were added afterward and passed a separate **1280-request** smoke and the **4000-request** first combined CI fixture. It showed lower allocations but worse p99 in all four stages. Full tables, CPU/allocation profiles, execution plans, pool/GC metrics and reproduction commands are in [performance.md](performance.md).

These were one fixed-order paired run on one host, not randomized sustained experiments. In-process HTTP includes client/harness costs. The native profile's CPU/allocation totals include setup/warmup and exclude PostgreSQL CPU; they are not peak RSS. The 4-worker stage is recovery from offered load, not a crash test. Realtime queue fan-out is a component benchmark. No network fault/storage outage, large restore timing, all-browser/accessibility review or exhaustive module audit is claimed.

The first image gate had residual MEDIUM/LOW/UNKNOWN records despite zero HIGH/CRITICAL findings. Public trust/timezone fixes and remaining package findings are documented in the security review. Existing custom extension/logger boundaries and external identity/mail providers need application-specific review. Automated passing gates do not eliminate these limits.

## Reproduce and continue safely

Use the checked-in scripts/workflows and exact source revisions, isolated test URLs, matching PostgreSQL clients and S3 fixtures. `full-qualification` opts a PR into the complete source/runtime suites; `source-qualification` opts only into source analysis. The scope parser refuses diff-informed results. Remove opt-in labels for later evidence-only commits after the qualified runtime completes, so a documentation update does not cancel the candidate's evidence-producing run.

SDK cookie, `session_changed`, template, OTP and dependency compatibility details are in [architecture and migration](architecture-and-migration.md). Existing main and operational environments remain untouched. Keep the PR a draft until the owner resolves the remaining broader qualification scope.
