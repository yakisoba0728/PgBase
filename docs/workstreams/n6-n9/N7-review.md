# N7 Dart SDK independent review

Review candidate: `85c249a3d4da6841a436169d59ecddd8f8f1b682`

N7 product commits: `cae25dc`, `2c97f31`, `85c249a` (worker commits
`11923c6`, `ed6fcde`, `52e23ce`). The scoped comparison is
`git diff 17cef23..85c249a -- sdk/dart docs/dart-sdk.md docs/workstreams/n6-n9/N7.md`.
The interleaved N6 commit and all N6 files are excluded.

Sources used for Spec review:

- `docs/workstreams/n6-n9/N7.md`
- `docs/superpowers/plans/2026-09-10-n6-n9-execution.md`, N7.1-N7.4
- `docs/next-work.md`, N7
- `docs/openapi.routes.json`
- `sdk/typescript/src/index.ts` and `sdk/typescript/src/admin.ts`
- the corresponding `internal/api` auth, record, batch, file, realtime, and
  error handlers

## Standards

No material Standards finding in the scoped diff. The repository has no
`AGENTS.md`, `CONTRIBUTING.md`, or `CODING_STANDARDS.md` in this checkout. The
new package stays under the N7-owned subtree, does not alter Go, TypeScript,
routes, migrations, generated files, or common status documents, and its
module sizes do not trigger the documented Go-only 800-line limit. The Fowler
smell baseline did not identify a correctness-relevant smell worth gating;
the repeated user/admin transport shapes preserve the intentional bearer vs
cookie/CSRF boundary.

## Spec

### P1 - logout can leave a stale persisted token

`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/auth.dart:28`
updates memory before awaiting `AuthStorage.write`, while `clear` independently
updates memory and awaits `AuthStorage.clear` at line 33. With a delayed write,
`clear()` completes first and the older write then stores the stale token. A
future `restore()` can revive the logged-out session. This violates the
generation/logout contract in N7.md lines 83-89 and N7.1.

Reproduction: block `AuthStorage.write("stale-token")`, call `clear`, release
the write, then inspect storage. Expected `null`; actual `"stale-token"`.

Narrow fix: serialize/version storage mutations so the newest generation is
the final durable state. Add save-vs-clear and restore-vs-clear races using a
delayed `AuthStorage`.

### P1 - an old admin 401 clears a newly adopted session

`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/admin.dart:82`
does not capture the request generation, and lines 98-103 clear cookie/CSRF on
every 401. If an old request returns 401 after a newer login succeeds, the next
mutation loses `X-PgBase-CSRF`. The TypeScript reference guards this cleanup
with the generation captured before send at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/typescript/src/admin.ts:158`.

Reproduction: login with `csrf-old`, hold a mutation, login with `csrf-new`,
then release the held request as 401. Expected the next mutation to send
`csrf-new`; actual CSRF header was absent.

Narrow fix: capture `_generation` before send and clear session state on 401
only if it is still current. Retain the existing generation guards for adopt
and logout.

### P1 - cancellation cannot stop a stalled file hash stream

`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/files.dart:153`
uses `await for` and observes cancellation only after a chunk arrives. If the
source stalls before its next chunk, cancellation cannot cancel the source
subscription or finish the upload operation, contrary to N7.3 and N7.md lines
175-177.

Reproduction: provide an open stream that emits no chunks, start `upload`, and
cancel after one event-loop turn. Expected `PgBaseCancellation`; actual future
was still pending after 100 ms.

Narrow fix: own the hash stream subscription, race it with the cancellation
future, cancel the subscription on cancellation/error, and close conversion
state deterministically.

### P2 - terminal SSE errors are retried

`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/realtime.dart:54`
turns the non-2xx response into `PgBaseError`, but the broad catch at line 83
retries it. A 410 `cursor_expired` must surface immediately under N7.md lines
156-162 and N7.3; invalid cursor, authentication, and access errors are also
terminal by the declared contract.

Reproduction: return 410 `{error:{code:"cursor_expired"}}` for every stream
request with `maxReconnects: 5` and zero delay. Expected one request; actual
six requests before the error surfaced. With default delays this also adds
7.75 seconds before the final request.

Narrow fix: classify terminal `PgBaseError` status/code values before the
reconnect branch and test 400/401/403/410 separately from transient transport
EOF/failure.

### P2 - realm profile exposes the server envelope instead of attributes

`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/client.dart:487`
returns the entire response for both profile methods. The server wraps the
payload as `{attributes: ...}` at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/internal/api/auth_collections.go:316`,
while the TypeScript contract unwraps it at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/typescript/src/index.ts:389`.

Reproduction: return `{"attributes":{"displayName":"Ada"}}`. Expected
`profile["displayName"] == "Ada"`; actual top-level key is `attributes`.

Narrow fix: validate the envelope and return its `JsonObject attributes` for
both GET and PATCH.

### P2 - schema preview and replay identity are not bound by the SDK

`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/admin.dart:121`
returns an unbound preview, and `applySchema` at line 130 accepts an unrelated
body plus caller-selected operation and idempotency values. Import has the same
shape at lines 155-178. This does not implement N7.md lines 103-108 or the
N7.2-admin requirement that a preview retain the exact approved bytes and one
stable replay identity. The TypeScript `ApprovedSchemaPlan` snapshot is the
existing reference.

Narrow fix: return an approved-plan value that snapshots the exact encoded
request before awaiting preview, retains the preview copy, and owns one stable
operation UUID/idempotency key used by every apply replay. Apply the same model
to schema import.

### P2 - structured errors omit the server request ID and network category

The server writes `error.requestId` at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/internal/api/errors.go:16`,
but `PgBaseError` has no field for it at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/errors.dart:1`
and decoding drops it at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/client.dart:544`.
The transport only maps `RequestAbortedException` at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/lib/src/transport.dart:143`,
leaving network failures as transport implementation exceptions. N7.1
requires status/code/details/request ID and distinct network/cancel/invalid-body
failures.

Narrow fix: retain `requestId` on SDK errors and map `package:http` network
failures to a stable SDK network exception without changing cancellation or
protocol error mapping.

## Verification and boundary

At the candidate SHA, Dart 3.13.3 `analyze lib test/sdk_test.dart
tool/live_conformance.dart` passed and the original seven `sdk_test.dart`
unit/wire tests passed. A temporary review-only test file reproduced the five
runtime findings above and was removed after execution; no product test or
fixture was committed.

The current live runner proves the happy user auth, create/update ETag, batch,
one SSE delivery, and file upload/list/token/download/delete path described in
N7.md. It does not yet prove the document's required revocation/logout, cursor
pagination, stale 412, idempotent replay/different-body conflict, exact JSONB,
batch rollback, reconnect/410, file cancellation cleanup, or admin 401/403
cases. The actual Flutter browser run is pending, and the committed Flutter
example at
`/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/review-n7/sdk/dart/example/flutter_app/lib/main.dart:6`
only renders an exact number and closes a client; it does not itself implement
the login/CRUD/file/realtime flow required by N7.4. These are completion
boundaries rather than additional Standards findings.

No live network fixture was started by this review. Ports 8090, 55440, 59000,
58091, 55441, and 58092 and the owner's `pgbase-n7` Docker project were not
used or changed. Product fixes require owner commits and focused re-review.

Current totals: Standards 0; Spec 7 product findings (three P1, four P2), plus
the listed live/browser acceptance boundary.
