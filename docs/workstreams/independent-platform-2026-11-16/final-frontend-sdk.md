# Final continuation: frontend and SDK audit

Base: published PR head `b376e36697674544be821be0290e24916d81da7d`.
This is an independent continuation; prior successful CI is historical evidence,
not qualification of the changes recorded here.

## Reproduced and corrected: Realtime subscription lifetimes

The TypeScript and Dart shared Realtime services checked authentication only
before waiting for a previous, aborted connection to drain. A pending subscription
could survive an account change during that wait, open a connection using the new
account's bearer, and install the old account's callback. Both also returned
cleanup functions that referred only to the topic and callback: an old screen's
cleanup could remove a replacement account's subscription when the same callback
was reused.

The added regression files reproduce both schedules using controlled in-memory
transports. The old transport's close is explicitly held until after the account
changes; there is no timing-based delay to guess the ordering. Against the
unmodified baseline, both TypeScript cases failed (`Missing expected rejection`
and `stopped` instead of `connected`), and both Dart cases failed for the same
reasons. The actual TypeScript reproduction dispatched `Bearer account-B` for the
subscription started under account A.

Subscriptions now retain the initiating authentication generation through drain
and readiness. Cleanup is bound to its original connection and listener set.
The TypeScript connect path also checks for a stop after asynchronous topic sync,
so an obsolete connection cannot report itself connected after it was stopped.
Neither SDK automatically transfers existing subscriptions between identities.

Validation after the patch:

- TypeScript targeted Realtime protocol/status/lifetime suites: **14 passed**.
- Dart targeted Realtime protocol/status/lifetime suites: **15 passed**.
- Complete TypeScript command (`npm test`), including build and public type
  contract: **111 passed**, zero failures/skips in the Node test-runner suite;
  the additional existing `sdk.test.mjs` script also passed.
- Complete Dart suite: **132 passed**; `dart analyze`: no issues.
- Generated TypeScript `dist/realtime.js` rebuilt from source. No UI design or
  public request/response schema changed.

Tests use Node 24 and Dart 3.13.3, disposable in-memory HTTP streams and synthetic
tokens, without network servers or production credentials. These are client
lifetime regressions, not server authorization bypass demonstrations.

## Reproduced and corrected: abandoned TypeScript SSE responses

The SSE iterator released its reader lock when its consumer exited, but did not
cancel the unread body. A malformed Realtime frame triggered reconnect while the
previous response remained open. Repeated failures could retain extra server
subscriptions and transport resources until the whole Realtime service stopped.

Two deterministic tests failed before the fix: explicitly leaving a consumer
called the stream's cancellation hook zero times; reconnecting after malformed
JSON opened a second connection while the first was still uncancelled. The
iterator now cancels the body in its cleanup, preserves the original error if
cancellation itself fails, then releases the reader. This does not change replay
or authorization semantics. Full TypeScript build, public type contract and test
command now pass **113 Node tests** plus the existing standalone SDK checks.

## Managed-backup browser coverage

The previously skipped managed-backup test has been replaced by a dedicated
`managed-backups-chromium` Playwright project. It has a third disposable Compose
project, database volume, origin and ports, separate from onboarding and normal
authenticated browser tests. It uses the same image selected by the E2E workflow
(including the digest-pinned release candidate mode), with local file storage
and a private backup subdirectory in a 256 MiB tmpfs. The subdirectory is created
by the nonroot runtime; the tmpfs root itself is not incorrectly chmod'ed by it.

The scenario signs in, creates a collection and record, uploads a file with
declared SHA-256, creates a backup through the UI, downloads and hashes the actual
archive, and uploads that archive through the UI. It then changes the record,
creates another record and deletes the attachment. Restoring the uploaded archive
must recover the original record value and ETag, remove the post-snapshot record,
and recover the exact UTF-8 attachment bytes through a protected download. It
also checks the typed restore confirmation, the specific returned restore job,
readiness and the visible restored record. A successful backup job alone is not
the acceptance condition.

Local verification completed: TypeScript checking of the new Playwright spec and
configuration, plus discovery of **26 browser tests in four files**, including
the new managed restore scenario. Docker is unavailable in this local environment,
and the known local Chromium IPC restriction is not bypassed. **Discovery is not
a browser execution result**; the shipped-image scenario must pass in the hosted
E2E workflow before release qualification. The workflow always supplies its
dedicated project URL; it is optional only for local developers who do not start
that third backend. Existing authenticated tests no longer contain the old
`PGBASE_BROWSER_MANAGED_BACKUPS` skip.

### Hosted E2E follow-up: Chromium inspector response eviction

Hosted runs [43795108138](https://github.com/yakisoba0728/PgBase/actions/runs/43795108138)
and [43797156393](https://github.com/yakisoba0728/PgBase/actions/runs/43797156393)
each passed the other 25 browser cases but failed this scenario while Playwright
read the upload response body using Chromium `Network.getResponseBody`. The
second run used `13db9a43`'s immediate response-read attempt and still failed.
That attempted fix was insufficient. The second backend log records the upload
as HTTP 201 in 19 ms and the subsequent archive-list GET as HTTP 200.

The original interpretation of Playwright's navigation hint was too specific.
In the installed Playwright 1.63.0, `Response.internalBody()` appends that hint to
any protocol error from its body callback; it does not establish that navigation
caused this failure. The UI consumes `XMLHttpRequest.responseText`, whereas the
test's `Response.json()` makes an additional CDP diagnostic-cache read. Chromium's
[`NetworkResourcesData`](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/third_party/blink/renderer/core/inspector/network_resources_data.cc)
counts request post data and response data in its resource cache and can evict
content under resource or aggregate limits. The failed runs did not record the
cache occupancy or fixture archive size, so the precise eviction trigger is
**not established**; a large upload is not claimed as the proven cause. The
fixture now logs the actual archive size and digest for subsequent runs.

The corrected test retains each real UI action and HTTP status assertion. It
waits for the UI's selected archive or job UUID, then checks that exact identity
through authenticated API reads outside Chromium's inspector cache. Upload also
requires the success toast, exactly one new repository entry, the selected
pane's ID and digest, and a second actual UI download whose size and SHA-256
match that uploaded entry. Upload verifies and repacks archives, so it is not
required to preserve the gzip container's original bytes. Both create and restore
jobs must identify the expected operation, archive and completed state. The
typed confirmation, post-snapshot mutations, restored record/ETag, removal of
the extra record, exact protected attachment content and readiness checks remain.
No browser test is skipped, mocked or retried by this correction.

The retained second-run failure excerpt is
[`hosted-backup-failure-43797156393.log`](evidence/final-frontend-sdk/hosted-backup-failure-43797156393.log).
The correction requires a new hosted browser execution; a source or TypeScript
check alone does not establish successful restore.

## Reproduced and corrected: administrator workflow ownership

Per-request session checks did not cover work performed before a request or
between requests. Twelve added failure schedules reproduced dispatch under a
replacement session or after cancellation:

- File hashing continued into upload after either the local session generation
  or the administrator identity changed.
- File replacement fetched an ETag in one session and sent its PATCH in another;
  selecting multiple files could send the second file after a session change.
- Bulk record deletion could start after its settings read finished in another
  session, or dispatch the queued records after the first six finished.
- Realtime administration could retry the two queued events after the first four
  finished across a session change.
- Schema delete could apply an old plan; schema apply/import could poll an old
  operation with the replacement session's credentials.
- The file query pool could dispatch after an account change while queued, or
  after cancellation in the interval between slot grant and continuation.

`captureSessionScope` reuses the existing transport's generation and administrator
identity checks. Multi-step callers retain that scope through hashing, queued
tasks, later requests and final publication. Record exports use the same boundary
before each page and before download; obsolete deletes do not modify a new
session's cache or navigate it. The file pool checks cancellation again after
its wait and always returns its slot. An already dispatched server mutation may
have committed; this client change does not promise to undo it. It prevents
further dispatch and obsolete publication.

The first ten new schedules failed against the prior code while fourteen
existing controls passed. Both pooled-task cases also failed before their fix.
After all changes, the complete UI suite passed **440 tests in 78 files**, with
TypeScript checking, production build and a subsequent `verify:dist` rebuild.
The added successful-upload control also verifies the file digest, metadata and
path remain intact. Authored markup, styles, labels and the current design were
preserved. The React review covered event-handler ownership, functional state
updates and hook ordering; no additional state subscription or library was added.

## Source and validation trace

Latest workstream runtime commit: `dcbfd4a2` (UI), following SDK commits
`97e7f7e8` and `440466b2`. The browser fixture is `737742c8`. Later changes to
this report and its logs are evidence/documentation only. These are local
workstream results, not certification of a subsequently integrated server HEAD.

Raw logs and command/environment metadata are in
[`evidence/final-frontend-sdk`](evidence/final-frontend-sdk/README.md).

| Boundary | Directly reviewed source | Verification in this pass |
|---|---|---|
| SDK Realtime auth, subscribe, drain, sync, stop and dispose | `sdk/typescript/src/realtime.ts`, `sse.ts`, relevant `index.ts` event/auth/file callers; Dart `realtime.dart`, `auth.dart`, `auth_scope.dart`, `client.dart`, `transport.dart`, `files.dart` | Controlled transport failures before fixes; TS113 / Dart132 complete suites; Dart analyze; public TS types; independent Security source review of lifetime fix |
| Admin request, recovery, identity and cache lifetime | `api/http.ts`, `api/session.ts`, `app/signout.ts`, `app/queries.ts`, `app/guard.tsx` | Existing transport/session tests plus explicit generation and identity-change upload cases |
| File upload/replacement and pooled token reads | `api/records.ts`, `collections/FileField.tsx`, `collections/queries.ts` and their tests | Hash/preparation, ETag-to-PATCH, multiple upload, slot grant/cancel and queued identity schedules |
| Record paging, bulk delete and export | `collections/RecordsView.tsx`, `collections/bulk.ts`, `collections/queries.ts`, `components/virtualRows.ts`, table/feedback primitives | Existing pagination/window/bulk/partial failure suite; replacement-session settings and queued-delete cases |
| Schema plans, apply and outcome polling | `schema/apply.ts`, related `api/collections.ts` and `api/schema.ts` contracts | Delayed plan and fake-clock collection/import outcome polling cases |
| Realtime administration retry queue | `ops/RealtimeView.tsx`, `ops/queries.ts`, `app/limit.ts`, `api/realtime.ts` | Four-running/two-queued failure schedule; existing partial retry/window tests |
| Managed backup UI and restore boundary | `storage/BackupsView.tsx`, `storage/BackupJob.tsx`, `storage/queries.ts`, `api/backups.ts`, related `pgbase/managed.go` and managed integration test; E2E workflow/config/spec | Typecheck, project discovery and source review of separate database/storage; actual browser restore pending hosted E2E |
| Keyboard and accessibility primitives | `components/Dialog.tsx`, `Popover.tsx`, `Tabs.tsx`, `Button.tsx`, `Field.tsx`, `Table.tsx`, `DataGrid.tsx`; existing keyboard tests | Existing jsdom tests included in440; no new browser/a11y-engine execution claimed |

### Compatibility and remaining qualification

No public API shape, DB schema or migration changes were required. Calls that
outlive their session now fail with existing session/cancellation errors.
Applications must explicitly subscribe after changing identities. The shared
Realtime delivery cursor remains global; applications starting a fresh snapshot
workflow can explicitly call `resetCursor()` before resubscribing. This work does
not change the server's replay, ordering or authorization guarantees.

The direct review above is **not a full screen-by-screen or cross-browser audit**.
Not directly completed here: every SQL-console/auth-settings form and translation,
Firefox/WebKit or mobile-device browser runs, automated axe scans, long-duration
browser heap/resource stress, and server-backed TS/Dart contracts on the final
integrated HEAD. The root qualification workflow and independent QA reviewer must
record their actual outcomes. SDK/UI unit success and Playwright discovery alone
do not establish large-service or complete release readiness.
