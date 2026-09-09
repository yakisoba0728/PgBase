# Dart SDK

`sdk/dart` is PgBase's local Dart and Flutter HTTP client. It uses the same
versioned API routes and error envelope as the TypeScript SDK; it is not a
PocketBase-Dart-SDK compatibility layer and has no public package release.

## User client

```dart
final client = PgBaseClient(Uri.parse('http://127.0.0.1:8091/'));
final session = await client.login('person@example.test', 'password');
final created = await client.records('notes').createWithEtag(
  JsonObject({'title': const JsonString('write once')}),
  'create-note-0001',
);
await client.records('notes').updateWithEtag(
  (created.record['id'] as JsonString).value,
  JsonObject({'title': const JsonString('updated')}),
  created.etag!,
);
```

The default `AuthStore` is memory only. Supply an application-owned
`AuthStorage` only when its implementation is appropriate for that platform
(for example, a secure mobile key store). Login, MFA and OTP completion use an
auth generation guard; logout, password change and email confirmation clear the
token locally even if their request fails. PgBase remains the authority for
revocation and expiry; the SDK never creates a refresh-token flow.

`PgBaseAdminClient` is separate. It accepts a cookie-capable `package:http`
client for its target platform and keeps only the CSRF token in memory. It never
reads a public-user `AuthStore`; same-origin browser administration and native
cookie configuration are the embedding application's responsibility.

## Exact values and cancellation

Use `JsonObject`/`JsonArray`/`JsonNumber` for arbitrary JSONB when number text
must survive exactly. `bigint` and `numeric` record fields are PgBase decimal
strings; use `ExactDecimal` rather than `double`. `PgBaseUuid` validates the
canonical UUID form. Record create requires an idempotency key, updates and
deletes require a strong ETag, and `CancellationToken` maps to `package:http`
abortable requests.

Record event delivery IDs are decimal strings. The SDK reconnects with
`Last-Event-ID`, includes cursor frames in its saved position, deduplicates event
UUIDs, and surfaces an expired replay cursor as the server's HTTP error so the
application can reload records.

## Local verification

The package is deliberately `publish_to: none`; Dart 3.13 has no `pub pack`
command. Verify the local consumer instead:

```sh
export PGBASE_DART=/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/.runtime/dart-sdk-3.13.3/bin/dart
export HOME=/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/.runtime/dart-home
export PUB_CACHE=/Users/yakisoba0728/Documents/GitHub/PgBase-worktrees/n6-n9/.runtime/pub-cache
(cd sdk/dart && "$PGBASE_DART" pub get && "$PGBASE_DART" analyze && "$PGBASE_DART" test)
(cd sdk/dart/example/dart_cli && "$PGBASE_DART" pub get && "$PGBASE_DART" analyze)
```

Run the Flutter widget example with the task-local Flutter runtime in the N7
worktree. Neither command publishes a package.
