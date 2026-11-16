# Final nonperformance QA and handoff — CI green, acceptance Blocked

## Decision and scope

The resumed nonperformance qualification completed: **14/14 jobs successful**, including Chromium **26/26**, Firefox **6/6**, WebKit **6/6**, hardened runtime, and **30 black-box top-level executions** (15 scenarios, `-count=2 -shuffle=on`). There are no unfinished jobs in that run.

**Final task acceptance remains Blocked, not Non-performance Ready.** The previous Firefox font abort did not recur in the completed qualification or either bounded diagnostic. Its root cause and a corrective browser/runtime change have therefore not been demonstrated. The requested independent child-agent QA was also not performed: this execution environment exposed no usable coding-subagent facility. Actual children created/completed/maximum concurrent: **0/0/0**. Hosted CI jobs and a second test run are not independent AI reviewers. The previous session's three agents remain historical evidence, not reviewers for this continuation.

This is a bounded partial completion with passing executable gates, not a claim that the original browser defect is fixed or that A-1/A-2 has been proved. No environment/browser defect was established merely from non-reproduction. The missing causal proof and unavailable independent-review capability are explicit acceptance blockers. **Overall product Not Ready — performance remains assigned to the user.**

## Exact source identities

| Item | Immutable identity |
| --- | --- |
| Original failing PR source | `56a881f47d0c27877b91959d7df641c2f603ef03` |
| Trace-upload correction / full-CI candidate | `527cc0a05f97ab24e089eb968cd70e92160ba973` |
| Actual successful CI synthetic merge checkout | `b48828a536b5b67436827e55ebdfd7e755dece52` |
| Candidate and CI checkout tree | `9c0313270d4a508777a6d7a2a88ee5450729d487` |
| Existing documentation checkpoint integrated | `fff75f80b5e049325ab8b84d89850dc8eb83237c` |
| Documentation-only integration / diagnostic test source | `5206f30b9bf0a1813d4b70b627b677548f13e09b` |
| Diagnostic source tree | `d4cba476c587865fe1a5b914ee75b8ecdb4fc392` |
| Main, unchanged | `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a` |

The synthetic merge has parents main and `527cc0a0`; its tree was checked directly. `5206f30b` adds only the earlier documentation/evidence checkpoint above the tested candidate. This report's checkpoint adds documentation/evidence only and uses `[skip ci]`; it does not claim a new full test execution on the documentation commit. PR #148 stays open, Draft and unmerged.

## Actual changes

`527cc0a0` changes only `.github/workflows/nonperformance-e2e.yml`, adding `ui/admin/test-results/` and `ui/admin/playwright-report/` to failed-run artifact paths. Existing Playwright `retain-on-failure` already produced traces; the old upload block omitted them. This repairs **evidence retention**, not the font-abort cause. Neither runtime nor browser assertions, console/CSP/5xx policy, retries, timeouts, selections or visual design changed.

The old documentation/evidence was integrated with merge commit `5206f30b` and retained unchanged. Two diagnostic workflow revisions exist only on `coordinator/firefox-trace-20261116-final`, without another PR:

- `88d4699ff17897fd4ac951b76a5bf8e493344a0f`: one immutable-source Firefox case, trace on, fresh disposable fixtures.
- `d055a48b8a40f4b2a2431f59618e427d105dd642`: the original Chromium suite first, then that same one Firefox case, to test the newly observed fixture-state hypothesis.

Both diagnostics check out `5206f30b` explicitly, retain original isolated onboarding/authenticated/backup origins, use random disposable fixture secrets, and remove only their own Compose resources. The diagnostic workflow is not integrated into the PR and is not a replacement release gate.

## Full nonperformance run

[Run 43845969233](https://github.com/yakisoba0728/PgBase/actions/runs/43845969233), attempt 1, completed successfully. The existing execution was awaited; no second full qualification was started merely to obtain green.

| Job | Job ID | Result |
| --- | ---: | --- |
| Go unit / static / formatting / vet / skip policy | 133226098165 | Success |
| PostgreSQL 16.13 | 133226098149 | Success |
| PostgreSQL 17.9 core | 133226097351 | Success |
| PostgreSQL 17.9 application | 133226097964 | Success |
| PostgreSQL 18.3 | 133226098055 | Success |
| Frontend | 133226097842 | Success |
| Browser and recovery | 133226097347 | Success |
| Security and supply chain | 133226097663 | Success |
| CodeQL Go | 133226097714 | Success |
| CodeQL JavaScript/TypeScript | 133226097335 | Success |
| macOS terminal | 133226097886 | Success |
| Windows self-update | 133226097927 | Success |
| Release-phase artifact contracts | 133226097674 | Success |
| Observability contracts | 133226097844 | Success |

The core PostgreSQL job steps include isolated S3, SDK analysis/test/live conformance and distribution reproduction; the PG17 core includes real-server administrator SDK consumers. Existing matrix/shard-specific skips remain unchanged. No prior source's successful job is substituted for this run.

The browser job log contains `26 passed` for unchanged Chromium and `12 passed` for the serialized Firefox/WebKit smoke projects (six each). Hardened-runtime verified `nonroot:nonroot` and the absence of `/bin/sh`. The black-box command completed with all 15 top-level scenarios twice, including backup restore after SIGKILL, failed-restore reconciliation, runtime dependencies, auth/CRUD/restart persistence, Realtime listener recovery and backpressure isolation, Jobs worker/scheduler crash recovery, PostgreSQL restart/read interruption, graceful shutdown, and fail-closed restore-parent termination. All isolated resources were removed successfully. No deployment or package publication occurred; the release-phase job is artifact testing only.

Fresh security jobs executed successfully. Existing `phase-nonperformance-security-ci.md` classifications and historical artifacts are preserved; this continuation does **not** claim a fresh independent finding-by-finding reconciliation or zero residual warnings.

## Firefox: what the new evidence does and does not establish

The original source failed in run [43836410887](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887), jobs `133194026551` and `133200243090`, with Pretendard subset 87 status `2152398850 / 0x804b0002 / NS_BINDING_ABORTED`. Functional session assertions finished; strict-console teardown failed. Its missing failure trace cannot be reconstructed from a later successful trace.

### Diagnostic 1: fresh fixture

[Run 43847672228](https://github.com/yakisoba0728/PgBase/actions/runs/43847672228): exactly one Firefox session test passed, zero skips/flaky/unexpected results. The preserved trace has no console errors/abort event. Crucially, it **does not request subset 87**, so this passing single-case run is insufficient to explain the old failure.

### Diagnostic 2: original Chromium prerequisite

The traced CSS includes underscore (`U+005F`) in subset 87. The original full CI runs Chromium before Firefox and leaves named collections, unlike the fresh fixture. That observation motivated one changed-precondition diagnostic, not an unchanged retry.

[Run 43848675663](https://github.com/yakisoba0728/PgBase/actions/runs/43848675663): Chromium **26/26** prerequisite and exactly one Firefox session test both passed, zero skips/flaky/unexpected results. The Firefox trace **does request subset 87** and records a complete **HTTP 200**, `font/woff2`, **24,664 bytes**, no request failure and no console error. A separate post-test GET of the exact resource also returned 200 with that size and MIME type. Thus a missing deployed file or consistently invalid URL was not observed. This supports the fixture-state difference but does not prove which text caused the request or why either historical attempt aborted.

Selected raw trace fields for the relevant test context (`1-trace.*`): subset-87 `_monotonicTime=3569.98`, HAR `time=0.121`, reload action `startTime=3663.75`, sign-out click `4032.569`, final collections navigation `4466.033`. These are causal-diagnostic trace fields, **not a performance measurement or benchmark**. No abort event exists in this run from which to establish navigation-cancellation causation.

No speculative synchronization, broad font filter, `NS_BINDING_ABORTED` exception, retry, skip, assertion deletion or timeout increase was applied. The observed success is recorded, while **NP-BROWSER-01 remains unresolved**.

## Raw evidence and integrity

| Diagnostic | Raw GitHub artifact | ZIP SHA-256 | Expiry returned by GitHub |
| --- | --- | --- | --- |
| Fresh | [14215408822](https://github.com/yakisoba0728/PgBase/actions/runs/43847672228/artifacts/14215408822) | `06fdf8b5f431ef4d99e32323286242d155bbe1c046fc220c57ee52d668948f64` | 2026-11-30 10:22:19 UTC |
| Chromium-primed | [14216248013](https://github.com/yakisoba0728/PgBase/actions/runs/43848675663/artifacts/14216248013) | `6107fdd8a060ea4f191102434fdd7084365f7f3301c9390f3ea94a1489957326` | 2026-11-30 10:28:58 UTC |

Both ZIP hashes were recomputed from downloaded bytes and matched the artifact API. Archives contain trace ZIPs, JSON results, console output, exact source/file hashes, font response and service logs. Raw traces may contain **disposable** fixture authentication values; do not repost their cookies/request bodies as public excerpts. Git-committed derived evidence intentionally excludes cookies, authorization, secrets, bodies and embedded font binaries. Actions raw archives have the explicit retention dates above; the compact provenance and timing ledger in `evidence/final-nonperformance-handoff/` is durable in Git. The old failed artifacts remain untouched.

The source manifest binds both diagnostics to unchanged SHA-256 values: `admin.spec.ts=9a5fe9276cd060024bf22e30e0c5f085ed8f326bf9dd7ad2f5a4ede08fc173ae`, `playwright.config.ts=c5d0e707a0b9c3508b2de502e6110a532417a6945b64747cc6dc71701afe4762`, package lock `19ca36afd61179505cd6f93f6c8df44d4a846471cc2464bef7247f41b7d95bcf`.

## Independent review, remaining actions and stop

No independent reviewer was created in this continuation; no worktree/agent is claimed. The coordinator inspected the narrow workflow delta, exact-source binding and retained results. That is **not independent QA**. No new runtime/security/data changes were integrated to justify reopening Backend/Storage or SDK audits.

Next acceptance work is limited to (1) an available independent coding reviewer checking the unchanged strict-console contract, fixture/source binding and this evidence; and (2) a new evidence-backed, bounded reproduction of the historical abort, retaining request start/response/failure, frame/navigation and console events before a minimal correction. A controlled request-lifecycle experiment may be proposed, but was not executed here and must be distinguished from the original failure. Do not repeat either existing diagnostic unchanged or rerun the complete matrix without a justified source change. The diagnostic workflow at `d055a48b` preserves the precise three-fixture setup and single-case command for review.

Schema 69 and all prior operational constraints remain: retain ambiguous-write tombstones, keep live-reference/legacy-worker fencing, and do not migrate source storage before writers are proven quiescent and durable references/intents reconciled. Real external OAuth/OIDC/SMTP providers and comprehensive accessibility/device coverage remain outside the tested scope. Existing security warning classification is not erased by green scanner jobs.

**Performance excluded.** Historical sustained qualification lost 547 of 120,000 scheduled arrivals at 200 RPS for 600 seconds. No new performance run, profiling, optimization or pass was performed. Original full/performance definitions, criteria, failure evidence and required-check configuration remain intact. 성능 검증은 사용자에게 이관하며 전체 제품 출시 판정은 **Not Ready**로 유지한다.
