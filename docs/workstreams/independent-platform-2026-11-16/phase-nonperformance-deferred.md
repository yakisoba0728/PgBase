# Nonperformance completion — unresolved and deliberately excluded work

This session is bounded to Storage, existing SDK/admin session ownership,
recorded security/correctness boundaries, and integrated functional validation.
It does not reopen performance engineering or a repository-wide audit.

| Item | State | Owner | Evidence / safe next action |
| --- | --- | --- | --- |
| **NP-BROWSER-01: Firefox strict-console gate** | **Blocked** | maintainer | On final source `56a881f4`, both original job `133194026551` and the single accepted retry `133200243090` fail the same login/reload/signout teardown: Pretendard font `NS_BINDING_ABORTED` (`2152398850`). Functional assertions completed; current coverage is Chromium 26/26, Firefox 5/6, WebKit 6/6. Retain a failing Playwright trace and console/request timing before a justified narrow correction; no blanket console filter or timeout relaxation. |
| Final-source hardened-runtime / black-box completion | Blocked by NP-BROWSER-01 | maintainer | These downstream steps did not run after the smoke failure. Historical `30523dda` hardened-runtime and 30 black-box passes are separate evidence, not current-source qualification. Re-run the affected browser/recovery job after resolving the blocker. |
| Sustained HTTP zero-loss gate | Deferred, unresolved | **user** | Historical run [43820007603](https://github.com/yakisoba0728/PgBase/actions/runs/43820007603) completed 119,453 of 120,000 arrivals with 547 queue-full drops at 200 RPS for 600 seconds. No new performance execution or pass is claimed. |
| Performance investigation and benchmarks | Explicitly excluded | **user** | No mixed-http/open-loop rerun, profiling, pool/WAL tuning, CPU/RSS optimization or benchmark baseline measurement in this session. Existing workflow definitions, criteria and historical evidence remain intact. |
| Ambiguous remote object writes | Documented operational limit | operator | Retained tombstones have no arbitrary expiry. Source-storage migration remains blocked until writers are provably quiescent and durable references/intents are reconciled. See `phase-nonperformance-storage.md` and `docs/storage-settings.md`. |
| Real external OAuth/OIDC and SMTP accounts | Deferred external interoperability | operator | Local fake-provider/mail contracts only; no live provider account or production credential was used. Revalidate the intended provider in a separate controlled acceptance environment. |
| Comprehensive assistive-technology, mobile and browser coverage | Deferred outside representative suite | maintainer | Chromium regression plus opt-in Firefox/WebKit representative login, record, focus and backup flows do not establish coverage of every browser/reader/device combination. |
| Exhaustive policy and multipart/batch combinations | Deferred outside selected contracts | maintainer | Auth metadata, principal isolation and atomicity regressions passed; arbitrary expression/body/header/query combinations require a specified additional contract and narrow reproductions. |
| Extension failure after a successful SQL commit | Documented ambiguous-outcome limit | maintainer | A later VM or transport failure does not imply database rollback. Every VM/host adapter combination is not certified; preserve idempotency/ambiguity semantics. |
| Native-helper vulnerability reachability and scan exclusions | Open applicability limits | maintainer | Preserve the final all-severity image and source records and their existing classifications; successful scanners do not establish absence of every native-library risk or cover excluded languages/platforms. |

## Qualification execution limits

Local disposable PostgreSQL testing initially used an SQL_ASCII cluster by
accident; the populated Unicode migration test exposed that fixture error. A
corrected explicit UTF-8 cluster is used for the final local affected-package
run. Failed attempts remain in the Storage evidence.

Shared temporary tool storage filled during a local MinIO build, and concurrent
compiler/test workers were killed. The attempted MinIO build was stopped. Only
its new download and reproducible npm download cache were removed; existing
worktrees and artifacts were preserved. Original resource failures are retained.
Hosted nonperformance CI supplies isolated MinIO and a fresh compatibility
matrix; local missing fixtures are not recorded as successful tests.

Final hosted PostgreSQL 16/17/18 and isolated MinIO all passed, closing the local
fixture coverage gap. The final nonperformance workflow is **13/14 successful**,
with only the repeated browser job failure above. There are no unfinished jobs.
The user's repeated-defect stop boundary was applied; no further retry, blind
test relaxation or broad investigation was started.

## Release decision

**Not Ready.** 성능 검증은 사용자가 직접 처리할 예정이므로 미완료로 남겨둠.
전체 릴리스 판정 Not Ready 유지.

Nonperformance implementation is integrated, but nonperformance qualification
is **not fully complete** because NP-BROWSER-01 and its downstream unexecuted
checks remain. The overall release decision is blocked by these as well as the
separate user-owned unresolved performance gate.

The dedicated nonperformance checks do not replace the original full-source or
performance gates, and no required-check setting is changed. Removing the
`full-qualification` execution opt-in label is solely to avoid automatically
starting prohibited performance work when publishing this session's source.
The original failed result remains the only sustained-load qualification.
