# Native SDK correctness — 2026-09-22

This work preserves PgBase's own API and opaque session-token semantics. It does not add PocketBase-only services or claim drop-in compatibility. Prior comparison evidence under `docs/audits/2026-09-22-pocketbase-comparison` was not rewritten.

## Implemented contracts

- TypeScript parsed public/admin responses preserve failures while consuming the response body. Malformed success JSON rejects with `invalid_response_body`; 204 remains a valid empty success. Exact JSON record methods retain their verbatim text, auth, ETag and idempotency behavior.
- TypeScript `AuthStateStorage` / `browserAuthStorage` optionally persist a JSON token/record snapshot. The existing `TokenStorage` / `browserTokenStorage` contract remains token-only, using its original key. Dart adds `AuthStateStorage` and immutable `AuthState`, selected by `AuthStore(stateStorage: ...)`; existing `AuthStorage` stays token-only. Dart storage operations retain serialization and generation protection against stale restore/clear races.
- Public-client GET/HEAD service requests now participate in automatic cancellation through body consumption. Default groups include method and the exact serialized request path/URL, collection, ID, and query/projection; query order is not normalized. Writes remain independent unless explicitly keyed. TypeScript uses `requestKey: null` for individual opt-out and a boolean `autoCancellation` property. Dart uses `autoCancel: false` on request/read options and `autoCancellation(false)` globally; its existing empty explicit key still chooses the default group. Explicit caller cancellation is preserved. Keys are never sent to the server.
- TypeScript `CollectionScaffolds` matches the server's schema-document templates, including the paired `auth: { collection, authCollection }`, rather than representing them as plan/apply changes. Existing exact record ID and auth schema transfer types remain exported.
- Both multiplex realtime clients recognize terminal `PB_ERROR` frames (`{ "code": ... }`). Initial failures preserve `cursor_expired` / `invalid_cursor`; post-readiness errors stop reconnection. Numeric checkpoint IDs stay exact and repeated IDs do not suppress overlapping topic deliveries. The existing callback API still does not notify callers of asynchronous errors after subscription readiness; adding an error/status callback is separate product work.

## Red → green evidence

Public API tests were added before implementing each new contract. Body interruption tests initially reported a missing rejection. The full-state storage tests initially failed because the new API was absent. Dart service cancellation reproduced `aborted == false` for an older duplicate read. The TypeScript scaffold compile test failed because `.auth.collection` and `.auth.authCollection` did not exist in the public type. Multiplex realtime tests reproduced loss of the server error code and repeated reconnection after a terminal frame in both SDKs. All pass after the corresponding changes.

Tests exercise injected external fetch/HTTP transports and caller-selected storage. Existing raw JSON tests additionally use a real local HTTP server. Test cases cover body abort/network errors, complete-body cancellation lifetime, query independence, explicit keys, global/per-read opt-out, uncanceled writes, old storage compatibility, snapshot restoration, Dart storage races, exact JSON precision, ETag/If-Match, idempotency, and realtime checkpoint/error handling.

## Final verification

- `cd sdk/typescript; npm test`: passed. Includes TypeScript build, strict source and generated public API checks, existing `sdk.test.mjs` conformance script, and **28 Node test entries**, with all newly added suites included in the normal test command.
- Official runtime: **Dart 3.13.0 stable, linux_x64**, from Docker `dart@sha256:8b6175f6c6b89aaf31ffdace4a22d17715c07f1cf3a772dadb10c658f779e23d` (tag `dart:3.13.0`). A disposable container mounted `sdk/dart` at `/workspace` and `.local/dart-pub-cache` at `/root/.pub-cache`. No user container was stopped.
- In that container, `dart pub get`, `dart analyze`, and `dart test`: succeeded; final analyzer **No issues found**, **47 tests passed**. The package had no pre-existing local lockfile. Resolution generated an ignored `sdk/dart/pubspec.lock` with 49 dependencies; no manifest or tracked lockfile changed. Resolved examples: `http 1.6.0`, `crypto 3.0.7`, `test 1.32.0`, `lints 6.1.0`.
- Dart changed files were formatted with the same pinned runtime. `git diff --check -- sdk/typescript sdk/dart`: clean.
- Split large modules without changing public imports: TypeScript `types`, `auth-store`, `realtime`, `sse`, `response`, `state-storage`; Dart `auth_protocol.dart` is a part of the existing client library. TypeScript generated JS/declarations were rebuilt. Largest production SDK files after splitting: TypeScript `index.ts` 721 lines, Dart `client.dart` 629 lines, both below the 800-line policy.

## Scope and remaining limits

No backend/UI production files were edited by this SDK workstream. Cancellation does not undo an already accepted server mutation. Storage implementations still choose security and durable encoding. TypeScript full-state persistence is synchronous; Dart is asynchronous and restores explicitly. Automatic cancellation does not manage administrator requests or long-lived SSE streams; Dart file downloads retain their explicit token path. Arbitrary JSONB precision still requires the opt-in TypeScript text methods, while Dart keeps its exact JSON value model.
