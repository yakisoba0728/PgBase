> Agent report snapshot. Local worktree paths and raw log filenames describe the agent’s scratch evidence at review time; those raw files are not all included here. Published source identities are recorded in [the commit map](../runtime-commit-map.json), and current combined CI evidence is in [validation](../../validation.md).

# Independent Frontend / SDK agent evidence

## Scope and provenance

Worktree: `/workspace/scratch/2a97293516a0/PgBase-frontend`.
Branch: `audit/frontend-sdk-20261116`.
Starting commit: `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b`.
Final local commit: `5bb0b580af6a129f7013227a12b3c9e7b8f3cf95`.

This was an actual child agent separate from the root. Owned paths were
`ui/admin`, `sdk/typescript`, `sdk/dart`, `examples/javascript` and related
example/UI/SDK tests. Root owns GitHub publication, workflows and central
validation documents; this agent did not push, merge, deploy or access an
operational database. Worktree was clean after the commits below.

Runtime used: Node v24.22.1, npm 11.9.0, Dart 3.13.3 (root-provided verified
toolchain). Dependencies came from existing lockfiles via `npm ci` and Dart
`pub get`. No dependency versions were deliberately changed.

Read the workstream handoff, validation/performance scope, root/SDK documentation
and actual relevant source/diff. Reviewed the recovered original JavaScript
SARIF/findings at `evidence/js`. The Vercel React best-practices skill was read
and applied to the changed TSX test/component call boundaries; no design system,
layout, CSS, or production React component was replaced.

## Original CodeQL JavaScript findings: three records, two root causes

The corrected `js/findings.json` contains the following rules and locations.
The scanner's securitySeverity is its rule metadata, not this agent's assessed
exploit severity. This agent did not run a new CodeQL scan locally and makes no
claim that the patched branch now has zero findings. Root owns run/commit/query
count reconciliation and the new CI scan.

| Original record | Classification | Evidence and action |
| --- | --- | --- |
| `js/incomplete-multi-character-sanitization`, `ui/admin/src/collections/relation.ts:56` (warning; scanner severity 7.8) | False positive for the current executable call graph | `htmlText` creates a plain-text summary, not trusted HTML. Its entity decoding can intentionally leave literal markup. `fields.ts` routes its result through a text cell in `RecordCell`; `recordLabel` routes it to React children/attributes in `RecordCell` and `RelationPicker`. Those sinks escape text. Added explicit contract comment and five rendering regressions covering malicious nested/encoded markup in editor cells, relation cells and selected relation labels. No sink was changed to HTML. Future reuse in `innerHTML` would invalidate this classification. |
| `js/client-side-request-forgery`, `examples/javascript/public/app.js:7` (error; scanner severity 5.0) | Genuine example credential destination trust flaw | A crafted example link supplied `?api=attacker-origin`; the page used that untrusted value as the prefix for password login requests. An attacker-controlled API can permit the CORS preflight. Exploitation requires the user to enter credentials into the example. Changed the destination to a deployment-owned static asset setting, defaulting to same origin. |
| Same rule, `examples/javascript/public/app.js:25` | Same destination flaw in the SSE path, not a second independent account takeover | The SSE Authorization header used the same query-controlled base. Fixed by the same static base. The tested fixture proves destination redirection, but does not independently prove theft of a legitimate-server token: the example originally also obtained its login token from that selected destination. |

No sanitizer algorithm or warning suppression was added just to silence CodeQL.
The HTML classification is backed by rendering tests, while the example flaw
has a failing-before execution of the real script with an attacker query.

## Validated defects and fixes

### Example destination and SSE lifetime

`examples/javascript/test/browser-security.test.mjs` uses Node's VM to execute
the real browser script with a minimal DOM and controlled fetch/stream behavior.
All four new tests failed before the fix and passed afterward:

1. An attacker `?api` cannot choose the destination of an entered password.
2. An attacker `?api` cannot choose the destination of an SSE bearer header.
3. If the mutation awaited by `nextRecordEvent` fails, the opened stream is
   aborted, the reader is canceled, and its lock is released.
4. Refused SSE HTTP responses abort the request as well.

Before, the mutation action was outside the cleanup `try/finally`; an action
failure leaked the open SSE reader. The `try/finally` now covers fetch, response
validation, reader acquisition, action and consumption. Cleanup failures do not
replace the original operation result.

Cross-origin development now changes the static `apiBase` in the served asset.
The example README documents that `?api` is ignored, gives the same-origin and
local cross-origin setup, and keeps the disposable-development-user guidance.

Logs: `frontend-example-before.log` (0/4), `frontend-example-after.log` (4/4).

### Administrator SDK session lifetime

The earlier code protected only selected CSRF/cookie mutations with generation
checks. It could still return old JSON, exact-text schema exports, backup bytes,
old login identities and old transport/HTTP failures to a caller after a newer
session was adopted.

TypeScript now routes JSON/text/blob handling through one `readResponse`
boundary. It checks the generation after fetch, after body consumption and on
failure. A replaced operation throws `AdminAPIError(403, "session_changed")`.
Current-session errors preserve status/details, and a current 401 invalidates
that session's CSRF generation. `adopt` also checks before returning/adopting.

Dart routes administrator JSON, backup upload and backup download through a
shared `_send` boundary. The transport already collects the full body; `_send`
checks the generation before accepting success, HTTP failures or cookies, and
rechecks when body transport throws. `_adopt` checks before returning/adopting.
Stale operations throw `PgBaseError(403, 'session_changed')`.

The targeted combined TypeScript admin/cancellation test set had 11 failures
before fixes, then passed 37/37. All seven new Dart admin lifecycle tests failed
before, then passed after. Tests retain newer-session cookie/CSRF assertions and
current-session error details; old successful-result expectations were replaced
with explicit obsolete-result errors rather than removed.

Logs: `frontend-sdk-before.log`, `frontend-sdk-after.log`,
`frontend-dart-before.log`, `frontend-dart-full.log`.

### TypeScript caller cancellation

Public low-level `send(path, init, options)` silently lost `init.signal` when
`options.signal` was absent, and one of two supplied signals could be ignored.
Both caller signals now participate through response-body consumption along
with keyed automatic cancellation. The existing request-controller mechanism
forwards the original reason, deduplicates identical signals and removes
forwarding listeners in `finally` after success/failure.

Root independently identified an unnecessary `AbortSignal.any` runtime
requirement in the first patch. It was replaced with `AbortController` plus
listeners. A new regression deliberately makes `AbortSignal.any` unavailable:
it failed before this compatibility correction and passed after, for keyed and
unkeyed success/transport-error cases, checking no forwarding listeners remain.

Logs: `frontend-signal-compat-before.log`,
`frontend-typescript-compat-final.log` (79/79 at that stage).

### Public bearer SDK isolation, including file preparation

Root requested a follow-up on the analogous public-user boundary. This agent
reproduced it with shared `AuthStore` objects: response-body consumption under
user A could finish after user B was saved and publish A's record/text/file or
an old HTTP/transport error. Existing auth-state mutation guards alone did not
guard returned data. Separately, a file upload started under A could wait for
file reading/hashing, then build request headers using B's token and submit the
file under B.

New tests deliberately delay body completion, change shared auth through a
second client, then release successful, 401 and transport-error bodies.
TypeScript exercises parsed records, exact JSON, record writes and file
multipart uploads. Dart exercises generic JSON, record results, batch multipart,
file upload and file download. Both additionally test old public/realm login
responses and delayed file preparation. All **15 TypeScript** and **18 Dart**
new tests failed before their fixes, and all passed afterward.

TypeScript's central `readResponse` now captures the shared store's generation
before dispatch, checks it before/after headers and body, and rechecks errors.
The upload helper captures the same boundary before `arrayBuffer`/hashing and
checks before dispatch. Dart uses an internal, unexported-from-package
`AuthScope` helper across request/record/batch and separate file transport paths.
Upload hashing and HTTP share one captured scope; a replacement identity causes
rejection before an HTTP request can start.

Both SDKs use `PgBaseError(403, 'session_changed')` for obsolete public results.
The original guard against overwriting a newer saved token remains. Current
login/refresh still adopt their own response, refresh-before-logout still sends
the rotated token, and a newer explicit login still prevents the old logout.
Existing race tests now additionally require rejection of stale refresh/auth
results, keeping the original newer-token/no-old-logout assertions.

Intentional semantics documented in both SDK READMEs: **any auth generation
change, including successful refresh for the same user, invalidates concurrent
older results**. Callers discard the result/cache and may start a fresh read.
Writes may already have committed; they must not be automatically replayed.
The guard does not stop server-side work or revoke previously returned download
URLs. Caller-owned direct `fetch`/Dart `transport` requests remain caller-owned.
No public signatures were removed and no SDK version/package was published.

Logs: `frontend-bearer-ts-before.log` (0/15),
`frontend-bearer-ts-after.log` (15/15),
`frontend-bearer-dart-before.log` (0/18),
`frontend-bearer-dart-after.log` (18/18),
`frontend-bearer-typescript-full.log`, `frontend-bearer-dart-full.log`.

### Independent security review: final adoption and result continuation

The independent Security Agent found a blocking gap in the first bearer patch
before candidate publication. Two TypeScript login responses released together
could both pass their body guards before either auth continuation resumed. The
first continuation saved session A; the second silently skipped its save yet
returned session B. The Security Agent also reproduced Dart login A returning
its old session after its asynchronous storage write was delayed and another
`auth.save` installed B in memory. These are concrete corrections to the first
patch, not claims that its original body-only tests proved the complete flow.

This agent added failing-before coverage for TypeScript public/realm login and
registration, public/realm OTP, MFA, OAuth and two refreshes sharing one store.
Additional tests cover reentrant persistence success/failure and actual
concurrent login plus record/upload completion. All 15 failed before; all 15
pass after. TypeScript now checks at final adoption, accounts for exactly its
own save increment, and checks again after caller-owned persistence/listeners.
`inAuthScope` includes asynchronous response mapping; file upload checks again
at its final SDK continuation.

Dart uses the same `AuthScope` across decoding, adoption and final completion.
Its `adopt` operation advances only the expected local generation for its own
save, then checks through asynchronous persistence. Nine auth completion paths
each have successful/failed delayed-storage replacement cases: all 18 failed
before and pass after. Two controls continue to preserve the original storage
error when the self-adopted identity is still current (20/20 final). The naive
simultaneous-response Dart test was not claimed to fail; the demonstrated Dart
defect is the persistence wait.

The existing refresh/logout ordering, rotated-token revocation after a current
storage failure, and no-old-logout/newer-token assertions still pass. No
microtask delay or scheduling loop was introduced. The READMEs explicitly limit
the guarantee to SDK work before its result completes: an already-fulfilled
Promise/Future cannot be retracted when auth later changes, and applications
must clear their caches/subscriptions themselves.

Security independently reviewed both working diffs without a remaining blocker,
reran the TypeScript 15 and Dart 20 tests, and verified its original Dart storage
repro now rejects `session_changed`. It subsequently confirmed that immutable
commits `9010d059` and `5bb0b580` match the reviewed/tested files, the worktree is
clean, the documented limits are accurate, and the new TS suite is wired into
the standard test command. Its supplementary report is
`security-final-review.md`.

Evidence: `frontend-adoption-ts-before.log`, `frontend-adoption-ts-after.log`,
`frontend-adoption-dart-before.log`, `frontend-adoption-dart-after.log`,
`frontend-adoption-typescript-full.log`, `frontend-adoption-dart-full.log`,
`frontend-adoption-dart-analyze.log`, `frontend-adoption-clean-dist.json`.

### Generated TypeScript package drift

A normal build of the prior source found that tracked `auth-store` output still
represented older cookie behavior and omitted its new `auth-cookie` dependency.
Regenerated those outputs using the repository's actual TypeScript build; none
were edited manually. All subsequent source changes include their build output.

After final bearer fixes, moved the old generated directory aside and rebuilt
from an absent `dist`. SHA-256 comparison against the previous build was byte
identical. Exactly **18 generated files = 18 tracked files**, with no missing or
untracked generated outputs. Evidence: `frontend-typescript-clean-dist.json`
and `frontend-typescript-dist-before.json`. Root added a CI empty-dist/parity gate.

### UI rendering and pagination-test reliability

The original full UI run had 426/427 passing, with the existing UsersView
ten-page/back-to-start test failing its one-second DOM polling assertion.
Its isolated file passed 5/5. Inspection showed that `resetQueries` refetch
completion and TanStack's scheduled observer publication are distinct events;
the held list may still show the old 100 rows before publication.

The test now awaits the actual reset/refetch promise and a scheduled
`notifyManager` callback inside React `act`, then asserts the first user appears
and the back-to-start control disappears. There is no timeout increase, skip,
weakened row/window assertion or production UI change. Awaiting only the refetch
promise was tested first and still failed, which is preserved in
`frontend-ui-full-after.log`; the final observer-aware version passed two
consecutive full runs (427/427), plus typecheck.

Logs: `frontend-ui-full.log`, `frontend-users-isolated.log`,
`frontend-ui-full-after.log`, `frontend-users-after.log`,
`frontend-ui-final.log`, `frontend-ui-repeat.log`, `frontend-ui-typecheck.log`.

## Final local verification

| Command / boundary | Result | Evidence |
| --- | --- | --- |
| `node --test examples/javascript/test/browser-security.test.mjs` | 4/4 passed | `frontend-example-after.log` |
| UI focused relation/editor rendering + records tests | 23/23 passed | `frontend-html-rendering.log` |
| UI `npm test` | 427/427, 77 files, twice consecutively | `frontend-ui-final.log`, `frontend-ui-repeat.log` |
| UI `npm run typecheck` | exit 0 | `frontend-ui-typecheck.log` |
| TS standard `npm test` after final code | 109/109 Node test-runner cases; legacy HTTP fixture script and public API typecheck also exit 0 | `frontend-adoption-typescript-full.log` |
| Dart `dart analyze` | no issues | `frontend-adoption-dart-analyze.log` |
| Dart full `dart test --reporter expanded` | 130/130 | `frontend-adoption-dart-full.log` |
| TypeScript build from absent `dist` | all 18 files byte-identical; no missing/orphans | `frontend-adoption-clean-dist.json` |
| New Playwright project discovery | 1 test in 1 file discovered | `frontend-browser-discovery.log` |
| New Playwright spec/config explicit NodeNext typecheck | exit 0 | executed with `--types node`, no output beyond npm warning |
| New Playwright browser execution | blocked before test execution | `frontend-browser-contract.log` |
| `git diff --check` | exit 0 | final local tree clean |

The full SDK tests use local controlled HTTP/stream fixtures; they are not
PostgreSQL integration or real-backend qualification. UI jsdom emits an existing
nonfatal "navigation to another Document" warning. The UI test durations are
not performance benchmarks.

The new browser contract loads the real example assets and intercepts all
network, including allowing the hypothetical hostile origin's CORS preflight.
It is explicitly an isolated browser contract, not live-server E2E. The bundled
Playwright browser was absent. An attempted system `/usr/bin/chromium` launch
then failed in `process_singleton_posix.cc` with `socket() failed: Operation not
permitted` (before any page/test could execute). **No local browser pass is
claimed.** Root's existing CI E2E command includes the new project automatically.

## Commits for root integration

1. `4f259b518911020764d0010e8d30ddcee4c0a392` — example static credential destination and SSE cleanup, four fail-before regressions, migration.
2. `7f916f473185f0f2626a313a5c9799466b3e2935` — rich-text summary contract and five inert-rendering tests.
3. `34ed3755122da7f5af43d78fa9cfeece332ca8b6` — regenerated auth-cookie distribution from prior source.
4. `1ee82456cd7b4aa41b6037776c25d684b6454adc` — TS request cancellation and administrator result lifetime.
5. `6512396d956e71e836b56e5af2436aa079547c68` — Dart administrator result lifetime.
6. `a95257801010127773cfc446634a0ef3e037e8b0` — UI pagination test awaits observer publication.
7. `68c92a8403bc017b5d0b0e31fb4ea938b871c79a` — cross-review correction avoiding `AbortSignal.any` requirement.
8. `71367f12c2f8e61de4bdc5e9603f132efac91e8c` — isolated Playwright example destination contract/project (local runtime blocked).
9. `e37d14e54543c7c844c33ac5e0f37b214b768d07` — TS bearer generation isolation, upload preparation, migration and CI test entry.
10. `fea6f05841dad89f21861c3dc6719df61e58fe5c` — Dart shared bearer generation isolation across transport paths.
11. `9010d05908066846f3a6597ee21fda335b2b20ae` — TypeScript final adoption/result mapping boundary, independent-review regressions.
12. `5bb0b580af6a129f7013227a12b3c9e7b8f3cf95` — Dart final auth adoption through delayed persistence, independent-review regressions.

## Cross-review and remaining limits

Root independently reviewed the administrator shared-reader changes and ran
19 targeted integrated admin Node tests. Root caught the AbortSignal runtime
compatibility issue; the agent reproduced it before correcting it. Root also
requested the public bearer follow-up, which produced the additional verified
fixes above. The separate Security Agent then found the final adoption and Dart
persistence races and independently verified their corrections, as detailed
above. Root owns comprehensive candidate CI; local tests and the reviews here
do not preempt or substitute for its eventual outcome.

Existing cookie, exact-number, upload cancellation/backpressure, Realtime
auth-stop/reconnect-budget/cursor/consumer-error tests were read/run as part of
the suites. No new unproven claim is made about real network latency, real
browser cookie races, live backend contracts, all-browser accessibility,
production-scale rendering, or exhaustive security coverage.

Administrator generation isolation is local to the client; changes in another
browser tab require resume/login to become visible, and a JavaScript guard
cannot control the browser's own in-flight Set-Cookie handling. Generic result
guards reject on completion; they are not immediate cancellation or a server
transaction rollback. Completed values already handed to an application still
require application-owned cache/session management.

This agent did not measure or claim a backend performance improvement, run
PostgreSQL-version integration/race tests, or complete live browser/operational
recovery E2E. Those belong to root and the other agents' candidate validation.
