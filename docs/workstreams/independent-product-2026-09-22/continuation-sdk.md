# SDK continuation — realtime status and signup profiles

Executed on 2026-09-22 against the existing remaining-development worktree. This evidence adds to `sdk.md` without rewriting its earlier results. In particular, its then-outstanding post-readiness realtime error notification gap is now implemented. No Go/UI production files were edited by this workstream.

## Public contracts

Both SDKs now expose `realtime.onStatus(observer)`, `realtime.status`, and asynchronous `realtime.resetCursor()`. Existing record/application subscription callbacks retain their payloads and initial-readiness behavior. An observer receives the initial immutable snapshot and subsequent connecting/connected/reconnecting/stopped states. Reconnect notices include attempt and error; stopped notices distinguish terminal failure, exhausted budget, unsubscribe, and reset. Exact numeric cursor text and wire error codes are preserved. Observer removal does not unsubscribe records, and observers persist across session restarts/reset.

Synchronous consumer throws and asynchronous Promise/Future failures generate a notice with listener source and topic without interrupting sibling delivery or changing connection state. Application helpers report the original topic name. Observer failures are isolated rather than recursively notified. Async callbacks are deliberately not awaited; consumers own serialization of ordered work.

Terminal failures stop retrying and remove subscriptions. Ordinary resubscribe retains the cursor. `resetCursor()` stops all subscriptions, waits for the session to finish, and explicitly forgets the cursor; the application then reconciles current data and resubscribes. It neither fetches a snapshot nor recovers expired history. Reconciliation against concurrent writes remains application work. Repeated numeric checkpoint IDs do not deduplicate overlapping topic events.

The API/auth workstream confirmed the unchanged wire contract for optional custom signup fields: JSON body `fields` on default/realm password registration and OAuth completion. SDK options call this `profile` because `fields` already means response projection. TypeScript exports generic `AuthCreationOptions<T>`; Dart accepts exact `JsonObject` values. The options reach registration, OAuth completion, callback parsing and browser helpers, without changing old signal/cancellation arguments. Existing OAuth login/link with a nonempty profile is rejected by the server rather than treated as an update. Responses remain ordinary sessions; custom profile attributes are fetched separately. TypeScript uses its existing ordinary JSON serialization, while Dart preserves exact numbers.

## Regression evidence

New public-consumer tests initially failed for the absent status APIs and missing signup body fields. Additional TypeScript regressions reproduced and fixed:

- Successful but empty handshakes resetting the retry budget indefinitely.
- A graceful stream close after unsubscribe overwriting stopped state with reconnecting.
- Reentrant subscription from a connecting observer creating a second connection.
- Application callback errors reporting the internal wire topic rather than the application topic.

Dart retains the existing timing for readiness cancellation while notifying stopped state, and existing pending-handshake cancellation tests pass. Both SDKs test post-readiness terminal errors, explicit reset, exact cursor reuse/removal, callback/observer errors, bounded reconnect exhaustion, and observer reentrancy. Signup tests check body profile versus query projection, default and named realms, OAuth completion/browser forwarding, legacy omission, retained validation errors, and unchanged auth state after rejected input. Dart additionally checks `9007199254740993` survives the profile request exactly. Strict TypeScript consumers test typed profiles and reject passing profile creation options to login.

Independent review found two additional lifecycle edges. Public regressions reproduced a stale `connecting` snapshot delivered after a nested `stopped` transition in both SDKs, and a TypeScript reset from a reconnect observer waiting through an already-cancelled one-second backoff. Observer delivery now stops an older snapshot's remaining traversal when a nested transition supersedes it. The TypeScript reconnect wait resolves immediately for an already-aborted signal and cleans up timer/listener registrations. Dart's existing cancellation Future already handled the second case; it now has the same public regression test. These tests are included in the normal full suites below.

## Final verification

- `cd sdk/typescript; npm test` passed: build, strict source/generated public types, existing conformance script, and **37 Node test entries**. The two new suites are in the normal npm test command.
- The cached official Dart 3.13.0 image was reused by immutable digest: `dart@sha256:8b6175f6c6b89aaf31ffdace4a22d17715c07f1cf3a772dadb10c658f779e23d`. Changed Dart files were formatted; `dart analyze` reported **No issues found** and normal `dart test` passed **56 tests** in a disposable container.
- No dependency manifests or tracked lockfiles changed for the continuation. TypeScript generated JS/declarations were rebuilt; `git diff --check -- sdk/typescript sdk/dart` was clean.
- Largest production SDK files: TypeScript `index.ts` **723 lines**, Dart `client.dart` **648**, Dart `realtime.dart` **608**, below the 800-line ceiling.

Production changes: TypeScript `src/realtime.ts`, `src/types.ts`, `src/index.ts` and their generated dist; Dart `lib/src/realtime.dart`, `lib/src/client.dart`, `lib/src/auth_protocol.dart`. New tests: TypeScript `realtime-status.test.mjs`, `signup-profile.test.mjs`; Dart `realtime_status_test.dart`, `signup_profile_test.dart`. README examples document observer lifecycle, explicit recovery, profile input, and compatibility boundaries.
