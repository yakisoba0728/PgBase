# Nonperformance completion — SDK/frontend evidence and blocked browser gate

## Verdict and exact source

**Blocked.** The current source passes the frontend and live SDK checks, but the
Firefox session console assertion fails on both observed browser attempts.
The complete nonperformance qualification is **13 successful jobs / 1 failed
browser job**, not a completed all-green validation. The current hardened-runtime
and black-box steps did not execute after that failure.

- PR source: `56a881f47d0c27877b91959d7df641c2f603ef03`.
- Actual CI checkout: merge `0a517baea58bf65e2293f15fb0a9fb54321f7eb7`.
- Both Git objects independently resolve to tree
  `9b4a2e78540c3737f03f6fcc27398bfaa0424180`.
- [Qualification run 43836410887](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887).
- Verified frontend log: [job 133194027215](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027215).
- First failed browser log: [job 133194026551](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194026551).
- Failed retry log: [job 133200243090](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133200243090).

The Coordinator made **one accepted browser-job rerun request**, with unchanged
source, tests, console filters, assertions and timeouts. An earlier request was
rejected because the workflow was still running and did not execute a retry.
The provider's later job inventory includes new successful job IDs as well;
this report preserves the exact verified source-bound logs above and does not
assert that no other reusable-workflow job was reexecuted. The final inventory
is recorded centrally in `phase-nonperformance-validation.md`.

## Current-source results

| Gate | Observed result |
| --- | --- |
| Administrator unit suite | **449 tests in 78 files passed** |
| Browser-example security tests | **4 passed**, zero failed/skipped |
| Typecheck and production build | Passed; rebuilt from empty distribution directory and compared exactly with committed output |
| Chromium | **26/26 passed** in each observed browser attempt |
| Firefox representative flows | **5/6 passed**, the same session teardown fails in each attempt |
| WebKit representative flows | **6/6 passed** in each attempt |
| Combined browser result | **37 passed / 1 failed** per attempt |
| Managed-backup browser recovery | **Three complete archive/file/record round trips passed** per attempt, within the successful browser cases |
| Hardened-runtime checks | **Unexecuted on current source**: step skipped after smoke failure |
| Black-box recovery suite | **Unexecuted on current source**: step skipped after smoke failure |

The browser logs record actual executions: `26 passed`, then `11 passed` and
`1 failed` in the twelve-case smoke selection. These are not discovery counts.
The remaining smoke cases cover schema creation, record CRUD/filter/delete,
command-palette navigation, mobile keyboard-focus containment/restoration and
managed backup. The archive cases verify size/digest, upload/repacking, snapshot
restoration and exact record/ETag/file contents.

## Live SDK contracts on the same source

The Storage agent downloaded each completed integration log once; this agent
independently read the SDK sections, verified the original log hashes and
checkout merge, and preserved concise excerpts.

| PostgreSQL | Verified integration job | TypeScript unit tests | Dart tests / analysis | Real-server contracts |
| --- | --- | --- | --- | --- |
| 16.13 | `133194027537` | **115 passed** | **134 passed; clean** | All four passed |
| 17.9 core | `133194027179` | **115 passed** | **134 passed; clean** | All four passed |
| 18.3 | `133194027244` | **115 passed** | **134 passed; clean** | All four passed |

The four server tests cover public-record HTTP behavior, relation/file/native
listing, text record identities and Realtime. The Realtime output explicitly
records both TypeScript and Dart subtests. PostgreSQL 17 additionally passed
`TestAdministrationSDKsAgainstRealHTTPServer` with both external TypeScript and
Dart consumers. Its separate application shard intentionally does not duplicate
SDK steps. Full integration ownership and evidence are in
[`phase-nonperformance-storage-ci.md`](phase-nonperformance-storage-ci.md);
this report retains `sdk-live-16.13.json`, `sdk-live-17.9.json` and
`sdk-live-18.3.json` with source/merge identities, exact lines and log hashes.

## Recurring browser blocker and stop boundary

Both browser attempts fail the Firefox case:

`session / rejects bad credentials, restores the session after a reload, and signs out`.

The functional login/reload/logout assertions complete. Its `cleanConsole`
teardown then records one identical downloadable-font error for
`PretendardVariable.subset.87-Lzui2vbK.woff2`, status `2152398850`
(`0x804b0002`, `NS_BINDING_ABORTED`). Mozilla's primary
[1914409 report](https://bugzilla.mozilla.org/show_bug.cgi?id=1914409) and
[289981 report](https://bugzilla.mozilla.org/show_bug.cgi?id=289981) confirm the
numeric error mapping. Cancellation during deliberate navigation is an
**inference**, not an established cause or proof that the console error is benign.

The first failure artifact `14210923584` was downloaded and its SHA-256 verified:
`52b63492ee4bdc0520b12fcb6c2c9da7852ee521e43e576a5f7d2f0836033fdc`.
It contains only the three Compose logs. Although the job output names a
Playwright trace, that trace was not uploaded. The exact font request is absent
from those server-log excerpts. The artifact and both complete browser job logs
are retained. The identical failure after the accepted retry is the concrete
reason to stop further repetitions without new evidence.

**Deferred next step:** reuse the disposable authenticated and backup fixtures,
run only the Firefox session case with a retained trace, and upload the actual
`ui/admin/test-results/**` files, including `trace.zip`:

```sh
PGBASE_BROWSER_SMOKE_ENGINES=firefox npm run test:e2e -- \
  --project=authenticated-smoke-firefox \
  --grep='rejects bad credentials, restores the session after a reload, and signs out' \
  --trace=on
```

Keep the existing isolated fixture URL environment variables. Inspect the font
request's cancellation and navigation timeline before choosing a correction.
Preserve the strict console/CSP/server-error assertions. This follow-up was
**not executed** in this session; no blind console filter, timeout increase,
assertion removal or additional retry was applied.

## Historical attempts remain separate

Source `30523ddaf27f8ffebd8b59c5ef802186107a408e`, merge
`495ea346467f5d90fc9d250531c9c8718233b6d3`, tree
`7a28c45d5d5bb46156140953332422c8cb13bbf3`, passed **38 browser tests**, three
managed-backup round trips, hardened-runtime checks and **30 black-box top-level
executions** in run `43834531061`. Those results are retained under
`evidence/phase-nonperformance-sdk-ui-ci/ci-43834531061/`. That run's integration
fixture still expected schema 68; its later SDK steps did not run. The only
source difference to the current candidate is the corrected doctor integration
test. Historical browser/black-box results are not substituted for current
source's failed or unexecuted gates.

The earlier source `d6cc73b8860dcca0b2bf175b9dfc2baee0c31c3f` had a browser
image-build failure in run `43833754019`: a 12,213-byte zlib download failed the
existing checksum before fixtures/tests ran. The original URL/checksum stayed
unchanged and subsequent builds verified the artifact. Its complete failure log
remains at the original evidence path; source/download verification is in
[`phase-nonperformance-security-ci.md`](phase-nonperformance-security-ci.md).

[`evidence/phase-nonperformance-sdk-ui-ci/manifest.json`](evidence/phase-nonperformance-sdk-ui-ci/manifest.json)
indexes the source-bound job logs, raw/compressed digests, executed counts,
artifact, SDK excerpts and prior attempts. Firefox/WebKit remain representative
coverage; no comprehensive visual/accessibility audit is claimed.

No performance workload was executed. Keep the PR **Draft**, the nonperformance
verdict **Blocked / incomplete**, and the overall release verdict **Not Ready**.
