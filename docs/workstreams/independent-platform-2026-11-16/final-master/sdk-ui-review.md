# SDK and administrator UI final review

Status: **LOCAL_FIXES_VERIFIED_FULL_UI_CI_REQUIRED**. Implementer validation is complete. The coordinator owns independent review, real-server conformance and final integrated CI qualification.

## Scope and source

- Reviewed source: `242f80aea730ee483cc22798fb6450bdb3064081`, tree `b758d273142d75377c18011d240cd64929cb4c97`.
- SDK correction: `8ccf8ca7b5a7a973dc209563e2b3256966da89be`.
- Administrator correction: `b56b74022eced2c767429b435a2abc4031ec37ee`, tree `2c6942b22a709d019b2cb18726ac8dc0b2d97887`.
- Tests executed in the worktree before commit creation; scoped production/test contents are reconciled with these commits in the manifest. This is not an exact-checkout CI claim.
- Owner: actual subagent `/root/sdk_ui_quality`; isolated worktree `qa/final-master-sdk-20261117`.
- Runtime: Node 24.22.1, npm 11.9.0; Dart 3.13.3 (same version as the repository CI). Dart archive SHA-256 `549c182cffbdc6864df7509c16fec646c73fe6cb8a18c2cb572db1292f300cd7` verified against the published checksum.
- No remote mutations, new PRs, main changes, deployments, external-provider credentials or live user data were used by this reviewer.

## Reproduced defects and corrections

### SDK-01: queued refresh changes account ownership (P2)

A refresh queued behind another refresh or logout captured the authentication generation only after that wait. If explicit authentication changed from A to B during the wait, the old queued operation sent B's bearer token and resolved with B's replacement session. This is a client session ownership defect; this reproduction does not establish a server authorization bypass.

Both TypeScript and Dart now capture the generation and refresh origin when the operation is requested. After queue drainage, they accept only the original generation or rotations made by the same refresh chain. An unrelated login/logout returns `403 session_changed` before transport. The existing token-rotation/logout behavior and authentication storage failure handling remain unchanged.

- RED: two deterministic failures in each language (queue behind refresh, queue behind logout).
- GREEN: stale work does not send B's bearer or replace B's token. Three consecutive same-session rotations still use the preceding token, and logout revokes the final token. An operation explicitly requested after switching to B can still wait for obsolete A work and then refresh B.
- Production change: 7 TypeScript source lines, 11 Dart source lines; rebuilt TypeScript distribution.
- No API signature, wire format, schema or migration changes. Reverting the SDK commit restores previous behavior without a database migration.

### UI-01: obsolete Jobs and Storage migration callbacks affect a replacement session (P2)

The Jobs run callback and migration resume callback unconditionally displayed old request failures and invalidated the shared query cache after the administrator session changed. Deterministic deferred failure tests reproduced both; corresponding same-session controls passed. The HTTP layer's `session_changed` rejection did not itself prevent these UI callbacks.

The affected Jobs save/run and Storage migration preview/start/cancel/resume actions now capture their session at the click or submit boundary, check it before starting queued work, retain it across asynchronous results, and publish callbacks only while that session is current. Obsolete migration previews cannot enable the start control. The existing `captureSessionScope` utility is reused, following the previously qualified Backups implementation.

Additional checks cover stale success receipts, a queued job click whose session changes before mutation execution, and a stale preview. No design, layout, wording, API, dependencies, or accessibility semantics were changed. Required static output is regenerated from the same source.

## Bounded review coverage

- TypeScript/Dart generation scopes: request/error body decoding, delayed auth adoption, buffered iterators, token refresh/logout ordering, file preparation.
- Realtime subscription serialization, disposal after identity replacement, asynchronous listener failures, stream cleanup and cancellation were inspected and covered by the existing SDK suites.
- Administrator HTTP/CSRF scope, existing Backups scope, schema apply polling, Jobs and Storage migration callbacks were inspected.
- React quality skill (`vercel:react-best-practices`) applied to changed TSX lines: guards precede requests; state updates stay in event/mutation handlers; no new effects, network waterfalls, subscriptions, dependencies, or styling changes.

No speculative optimization or broad refactor was adopted. The changes fix measured correctness failures; they do not claim throughput, latency, memory, or bundle-size improvements.

## Evidence and commands

All local logs are under [`../evidence/final-master/sdk-ui/`](../evidence/final-master/sdk-ui/). [`manifest.json`](../evidence/final-master/sdk-ui/manifest.json) records every log SHA-256, source mapping, command, result and retention classification. Both corrected production trees and required generated output are committed. No historical evidence was removed.

- `npm --prefix sdk/typescript test`: baseline 119 tests; fixed source 123 tests, no failures/cancellations/skips, plus SDK legacy runner and public API type checking.
- `dart analyze`: no issues. `dart test --reporter expanded`: 142 passing tests.
- `npm --prefix ui/admin test`: baseline 78 files / 449 tests passed. Fixed source full run: **457 passed / 1 failed out of 458**, 77 files passed / 1 failed. All changed-file cases passed. The failure was `ApiView.test.tsx` / `offers a retry when the collection's rules fail to load`, which could not find the Retry button within its existing wait; its source was unchanged.
- One bounded isolated rerun: `npm --prefix ui/admin test -- src/apidocs/ApiView.test.tsx --maxWorkers=1`: **3/3 PASS**. No assertions/timeouts/project worker configuration changed.
- During the failed full run the shared 9-CPU host showed load averages 29.20/18.79/9.45. Resource contention is a plausible explanation, not a proven sole cause. Raw failed output and resource observation remain preserved; an isolated pass does not replace the required full final CI result.
- UI baseline and corrected build/typecheck passed. Corrected committed UI output passes `verify:dist` with zero drift. TypeScript committed `dist` also reproduces exactly.
- UI focused run: 14/14 passed before the final stale-preview control; the complete run included 15 cases in the two changed test files, all passed.
- The generated asset renames follow Vite content hashes and import references; manually authored changes are limited to two components and their regression tests.

To reproduce SDK RED on the base, create a detached worktree at the base SHA, copy only `sdk/typescript/test/user-lifecycle.test.mjs` and `sdk/dart/test/user_lifecycle_test.dart` from the SDK correction commit, install dependencies, then run `node --test --test-name-pattern='queued refresh' sdk/typescript/test/user-lifecycle.test.mjs` and `dart test test/user_lifecycle_test.dart --name 'queued refresh' --reporter expanded`. The old production code must reject neither stale queue case; the two tests fail. Do not copy corrected production files into the RED worktree.

## Independent review handoff

Cherry-pick the SDK and UI commits in that order, then the evidence commit. Reproduce the two RED queue cases on the base production sources with the new tests, run both SDK suites and the two changed UI test files, and inspect that captured scopes begin at event invocation. The coordinator must retain the failed full local UI run until the final exact-source CI supplies a complete passing gate. No additional local broad rerun was attempted.

## Limits

This report records implementer validation, not independent QA. Real PostgreSQL HTTP conformance, packaged binary and Chromium/Firefox/WebKit checks remain the coordinator's integrated gates. Local SDK transport tests use disposable in-memory fixtures; third-party OAuth/OIDC/SMTP interoperability is not established here. Historical Firefox font-cancellation causality is not changed by this work.
