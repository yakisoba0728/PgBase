# Final nonperformance validation — Blocked

## Decision

**Implementation is integrated; nonperformance qualification is not fully complete.**
The final source has **13 successful jobs and one failed browser job**.
There are no unfinished jobs. The browser failure reproduced after exactly one
accepted unchanged-job rerun request; no further retries or investigation were
started. Overall product release decision: **Not Ready**.

This follows the user's explicit stop boundary for repeated defects without a
new evidence-backed hypothesis. No assertion, console filter, timeout, required
check or performance gate was weakened.

## Exact source and execution

| Item | Identity |
| --- | --- |
| Draft PR | [#148](https://github.com/yakisoba0728/PgBase/pull/148) |
| Tested PR head | `56a881f47d0c27877b91959d7df641c2f603ef03` |
| Actual synthetic merge checkout | `0a517baea58bf65e2293f15fb0a9fb54321f7eb7` |
| Identical source/merge Git tree | `9b4a2e78540c3737f03f6fcc27398bfaa0424180` |
| Final qualification | [43836410887](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887) |
| Main, unchanged by this session | `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a` |
| Final reports | Documentation-only `coordinator/nonperformance-completion-20261116` checkpoint |

The PR source stays frozen at the tested head. Later checkpoint commits change
only documentation/evidence and do not claim another runtime qualification.
Publication used expected-head, non-force ref updates; every published tree was
checked against the corresponding local tree. The complete local/published
commit mapping is in `evidence/phase-nonperformance-coordinator/source-publication.json`.

## Final nonperformance gates

| Gate | Result |
| --- | --- |
| Full Go unit/race, formatting, vet, pinned static analysis, skip policy | Success |
| PostgreSQL 16.13 complete integration | Success |
| PostgreSQL 17.9 core/race and application/race shards | Both successful |
| PostgreSQL 18.3 complete integration | Success |
| Isolated MinIO, schema 69 install/reapply, 68→69 upgrade and actual schema-68 backup restore | Successful in the core matrix |
| TS/Dart SDKs and real HTTP contracts | Every core version: TS 115, Dart 134 + analysis, four live contract tests; PG17 external administrator consumers successful |
| Admin UI | 449 tests / 78 files, typecheck, exact clean distribution rebuild; four browser-example tests successful |
| macOS terminal and Windows self-update | Both successful |
| Observability and artifact contracts | Successful |
| Whole-source Go/JS CodeQL and dependency/image audit | Successful execution; retained findings classified below |
| Browser/recovery job | **Failed**, including the single accepted retry |

The dedicated workflow has 14 jobs; the grouped rows above do not create extra
jobs. `final-job-ledger.json` preserves the latest GitHub inventory. Detailed
source-bound logs retain their actual original job IDs; the rerun API returns
new bookkeeping IDs for successful jobs as well. Security/artifact IDs and
digests remained unchanged after the browser rerun, and both failure artifacts
are preserved. Ordinary Backend, Frontend, and Security PR workflows succeeded.

The original Full source qualification run is **skipped, not passed**.
Its performance work was intentionally not executed. Dedicated functional
checks do not replace the unresolved original release/performance gate.

## Browser blocker and exact coverage

On final source 56a881f4, both original job `133194026551` and retry
`133200243090` produced the same result:

- Chromium: **26/26 passed**.
- Firefox: **5/6 passed**; the login/reload/signout test completed its functional
  assertions but failed `cleanConsole` teardown.
- WebKit: **6/6 passed**.
- Failure: one downloadable Pretendard subset font message with status
  `2152398850 / 0x804b0002 / NS_BINDING_ABORTED`.
- Hardened-runtime and black-box steps were **not executed on this final source**
  because the smoke step failed first.

Navigation cancelling the request is an inference. The existing failure artifact
contains Compose logs but not the referenced Playwright trace, so the exact
cause is not established. Treat this as **NP-BROWSER-01: Blocked**, not as a
harmless warning or a passing test.

Historical source `30523dda` did pass 38 browsers, three managed-backup round
trips, hardened runtime and 30 black-box executions. Final source differs only
in the doctor integration test, but that historical execution is still not
substituted for the final failed gate.

A future bounded resumption should first retain a failing Playwright trace and
console/request timing for this single Firefox case, establish the font request
lifecycle, and correct the actual defect or fixture expectation with evidence.
Preserve session assertions and the strict-console boundary; do not blanket
ignore font failures. Then run the affected browser/recovery job. No automatic
continuation or further retry remains active after this session.

## Security results, not zero-finding claims

Fresh final-source artifacts contain **24 Go and one JS CodeQL result**.
Resolved rule IDs, messages, fingerprint multisets and primary locations match
the historical baseline; the only primary-location change is the migration
checksum line moving from 297 to 300. Go reordered results/table indices, so
raw or complete code-flow equality is not claimed.

The image has **32 retained records: 21 MEDIUM, nine LOW, two UNKNOWN;
zero HIGH/CRITICAL records**. Image results and rule metadata match the baseline.
All seven changed Go runtime paths and both changed TS/UI paths were extracted;
there were no warning/error extraction notifications. Native-helper
applicability uncertainty and language/platform coverage exclusions remain
explicit in `phase-nonperformance-security-ci.md`.

## Preserved attempts and concrete corrections

1. `43833754019`, source `d6cc73b8`: eight successful jobs, two failures,
   four cancelled on source supersession. Pinned lint found an obsolete
   production cleanup helper; it was removed while preserving the two active
   collector COMMIT/ROLLBACK regressions. The first browser image download had
   an invalid zlib response; the original URL/checksum later verified and
   succeeded unchanged.
2. `43834531061`, source `30523dda`: eleven successful jobs, three failed
   core PostgreSQL jobs, all from one stale latest-schema68 doctor test guard.
   The correction requires 69, preserves actual 67 rejection and adds 68 rejection.
   Author and independent PostgreSQL17 UTF8/race checks passed all 12 subtests.
3. `43836410887`, final source `56a881f4`: thirteen successful jobs and the
   recurring browser failure above. One initial rerun API request was refused
   because the workflow was still running; it executed nothing. Exactly one
   later browser-job rerun request was accepted and reproduced the error.

## Scope, ownership and final stop

Three actual child agents handled Storage, SDK/UI and independent QA/security,
with at most three children running simultaneously. Security's authored avatar
fix received separate SDK-agent review; author and independent review evidence
are distinguished in the reports.

Storage/schema69, schema68 restore compatibility, SDK/admin session ownership,
OAuth-avatar special-use boundaries, and the obsolete helper/doctor fixtures
are implemented and integrated. The existing admin layout is preserved.
See the Storage, SDK/UI and security reports plus their final CI supplements.

**성능 검증은 사용자가 직접 처리할 예정이므로 미완료로 남겨둠.
전체 릴리스 판정 Not Ready 유지.** Historical [43820007603](https://github.com/yakisoba0728/PgBase/actions/runs/43820007603)
lost **547 of 120,000 arrivals at 200 RPS for 600 seconds**. No new performance
execution/pass, profiling, CPU/RSS optimization or benchmark measurement occurred.
Existing original workflows and performance evidence are preserved.

The bounded session stops with NP-BROWSER-01 and its downstream unexecuted
checks recorded, alongside the separate documented external-provider,
comprehensive accessibility/policy/VM and native-risk limits in
`phase-nonperformance-deferred.md`. No main push/merge, force push, deployment,
release, tag or package publication occurred.
