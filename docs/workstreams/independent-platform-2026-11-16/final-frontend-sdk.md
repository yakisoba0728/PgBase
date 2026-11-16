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

## Coverage and remaining work

Directly traced in this pass: shared Realtime subscribe/start/readiness/topic
sync/event dispatch/stop/disposer boundaries in both SDKs, their authentication
stores and cancellation/transport callers; administrator session transport and
query-cache recovery; managed-backup browser exclusion and its console/API/job
callers. This does not yet constitute a complete screen-by-screen, cross-browser,
accessibility, entire SDK or server security audit. Further work is recorded in
subsequent sections when implemented and executed.
