# Nonperformance completion — SDK and administrator session ownership

## Source and decision

Recovered two existing unpublished corrections on the verified PR source
`a1a652289f0bc817f18db257282c758e66b4a672`. Their relevant parent runtime blobs
matched the current PR. Fresh tests on that unchanged runtime reproduced both
defects; no already-fixed behavior was rewritten.

| Preserved local change | Recovered source commit | Scope |
| --- | --- | --- |
| `33de5dbbe574315ae77d1790f4203548121db415` | `88a524e4653cfbcb7cedd4d841651390b5b02b01` | TypeScript/Dart listener failure ownership |
| `8721239dc370f12e139b92ffec7a482edc70f0ef` | `a60df3455bdcfb24fb11dd03f1d2790305aa6f3a` | Administrator backup callback ownership |
| New verification configuration | `0118f4c5777ae5943c75bb1a860b7d401d805057` | Optional Firefox/WebKit smoke projects |

The first full SHA above corrects a typo in the untracked historical Phase B
report. The recovered source/configuration tree is
`a87b7f3759d6ae0e8f2eadfb1068ecba35eedad6`. Subsequent evidence commits do not
change that runtime. The Coordinator records final integration and hosted CI
identities in `phase-nonperformance-validation.md`.

## Behavior

TypeScript and Dart capture the transport owner and authentication generation
when invoking a Realtime listener. An old synchronous throw or delayed
promise/future rejection cannot publish its error into a replacement session.
Current-session error reporting and observer isolation remain supported.

Administrator create/upload/delete/restore mutations capture their initiating
session before queueing. `scope.run` fences dispatch, and `isCurrent` fences
progress, notifications, cached jobs, invalidation and URL selection after
settlement. Cancellation/failure reconciliation still runs for the current
session. This is client callback isolation; it is not a claim of a demonstrated
server authorization bypass. No API, schema, markup or styling contract changed.

## Fresh author verification

Node was 24.22.1; Dart was 3.13.3. Existing Node modules were reused only after
both package manifests and lockfiles matched byte-for-byte. Dart dependencies
resolved offline without a tracked lockfile change.

| Check | Observed result |
| --- | --- |
| Unchanged runtime with only the three regression-test overlays | TS: 2 failures/2 controls pass; Dart: 2 failures/2 controls pass; backup UI: 5 failures/7 controls pass |
| TypeScript full test command | Build, public types, standalone script and **115 tests pass**, zero skips |
| Dart analysis/full suite | Analysis clean; **134 tests pass** |
| Administrator typecheck/full suite | **449 tests across 78 files pass**, zero skips, with two local workers |
| Clean TS and administrator distribution rebuilds | Both generated directories removed and rebuilt; tracked byte comparison and unexpected-output checks pass |
| Playwright configuration | Typecheck passes; default fixture discovery remains **26 tests**; optional smoke discovery is **12 tests**, with no Chromium selection |
| Invalid browser configuration | Unknown engine, duplicate engine and absent backup fixture each fail before execution |

Initial local attempts are retained, not counted as passes. The unrestricted UI
run lost ten workers with `SIGKILL` and had a RichText assertion failure; the
concurrent Dart runner did not produce its temporary compiler snapshot. A later
UI launch failed with `ENOSPC`. After the Coordinator cleared reproducible
package-download cache and this agent used a task-local npm cache, the complete
UI suite passed with two workers; the unchanged Dart suite also passed on its
single retry. No assertions, timeout thresholds or test selections were relaxed.

The exact before-overlay tree, commands, outcomes and compressed/raw log hashes
are in [`evidence/phase-nonperformance-sdk-ui/manifest.json`](evidence/phase-nonperformance-sdk-ui/manifest.json).

## Browser execution contract

The existing Chromium projects and their defaults are unchanged. The optional
engines require the existing isolated authenticated and managed-backup backend
URLs. They run login/session, schema prerequisite, record CRUD/filter/delete,
command-palette navigation, mobile keyboard-focus containment/restoration and
the complete backup archive/file/record round trip: six cases per engine.
All opted-in projects use one worker so schema changes and restores cannot
overlap across browsers.

After the normal Chromium command succeeds, run:

```sh
PGBASE_BROWSER_SMOKE_ENGINES=firefox,webkit npm run test:e2e -- --project='*-smoke-*'
```

This selects only the twelve smoke cases. No additional onboarding project
reuses the already-initialized one-shot setup backend. Local discovery is not
browser execution; the final combined hosted CI owns Chromium, Firefox/WebKit,
real-server SDK contracts and backup recovery results. See the final validation
document for their actual outcomes. Comprehensive visual/accessibility and real
external-provider interoperability are not claimed by this slice.

## Independent review

The separate `/root/recon` agent reviewed the exact SDK, backup and browser
configuration commits with no blocker. It independently executed four TS
lifecycle tests, four Dart lifecycle tests and twelve backup-view tests on the
QA combination: all passed, zero skips. Its evidence is recorded in
`phase-nonperformance-security.md` and that report's evidence directory.

This agent also independently reviewed the QA agent's existing special-use
avatar-address correction and executed six focused Go/race top-level tests.
They passed; the reviewed file hashes and output are preserved as
`independent-avatar-review.json` and `independent-avatar-review.log.gz` here.

No performance investigation, benchmark, load test, production access, release
or package publication was performed. Overall release readiness remains subject
to the user's unresolved performance gate and the Coordinator's final verdict.
