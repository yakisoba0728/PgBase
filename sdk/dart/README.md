# PgBase Dart SDK

`pgbase` is a local Dart and Flutter client for PgBase's versioned HTTP API.
It is intentionally not publishable and does not claim PocketBase SDK
compatibility.

Use a caller-selected `AuthStorage` only when the application has an
appropriate secure persistence mechanism. The default `AuthStore` is memory
only. Administrator sessions use cookies and in-memory CSRF state through a
separate `PgBaseAdminClient`; never pass a user bearer token to it.

The existing `AuthStorage` interface persists only the opaque token. To restore
the cached user as well, implement `AuthStateStorage` and configure
`AuthStore(stateStorage: storage)`, then `await auth.restore()` on startup.
`AuthState` holds the token and immutable `User` together. Configure one storage
interface at a time. Save, clear and restore operations are serialized; an old
restore cannot replace a newer in-memory state. A storage implementation chooses
its own encoding and secure persistence. `isValid` means a nonempty token is
present, not that it has been verified by the server.

Public-client GET/HEAD requests cancel an older pending request with the same
method and exact serialized URL, including the collection, record ID and query.
This remains active until the complete response body is read. Mutations remain
independent unless a low-level `request`/`recordRequest` supplies a `requestKey`.
Record `list`/`getList` options and `get`/`getWithEtag` accept a custom `requestKey`
or `autoCancel: false` to opt out. Cancellation metadata never goes into the URL;
query parameters are not sorted. An empty explicit key retains the default
method/URL grouping. `client.autoCancellation(false)` disables registration;
`cancelRequest(key)` and `cancelAllRequests()` cancel registered requests.
Caller `CancellationToken`s remain supported. Cancellation cannot prove that a
write was rolled back. Admin requests, file downloads and SSE streams use their
existing explicit cancellation paths.

## Realtime status and recovery

Existing subscription callbacks continue to receive record/application events.
Observe the shared connection separately:

```dart
final stopObserving = client.realtime.onStatus((status) {
  print(status.state);
  if (status.error != null) print(status.error);
});
await client.realtime.subscribe('notes/*', (RealtimeEvent event) {
  print(event.record);
});
// stopObserving() removes the observer without unsubscribing records.
```

`onStatus` immediately delivers an immutable `RealtimeStatus` snapshot;
`realtime.status` reads the current snapshot. `RealtimeState` distinguishes
connecting, connected, reconnecting and stopped. `RealtimeStopReason` distinguishes
terminal failure, exhausted retry budget, unsubscribe and explicit reset (the
initial stopped snapshot has no reason). Notices include the attempt and exact
string cursor. Recoverable errors report the next attempt; repeated empty
sessions consume the retry budget even after handshakes. Delivery to a subscribed
topic resets the budget. Terminal `PB_ERROR`/HTTP errors retain their code,
including `cursor_expired` and `invalid_cursor`. Initial readiness still rejects.

Event callback throws and failed Futures report `RealtimeErrorSource.listener`
with the original subscription/application topic, leaving other listeners and
the connection intact. Observer errors are isolated without recursive notices.
Async consumers are not awaited; serialize application work if order matters.

If an observer changes lifecycle state synchronously, the new snapshot supersedes
any older snapshot still being delivered to other observers. Reset or stop from
a reconnect notice skips the backoff even when cancellation precedes the wait.

Unsubscribe/resubscribe preserves the numeric replay cursor. After expired history,
`await client.realtime.resetCursor()` stops all subscriptions and discards the
cursor; reconcile current data and subscribe again explicitly. Reset does not
fetch a snapshot or recover expired events. Reconciliation with concurrent writes
belongs to the application. Observers stay registered across reset. Repeated
checkpoint IDs never suppress overlapping topic deliveries.

## Signup profiles

```dart
await client.realm('members').register('person@example.com', 'password',
  profile: JsonObject({'displayName': const JsonString('Person')}),
  fields: 'id,email');
await client.oauthComplete('github', state, code,
  profile: JsonObject({'displayName': const JsonString('Person')}));
```

The optional `JsonObject profile` maps to the body's custom `fields` object,
preserving exact `JsonNumber` values. The existing named `String fields` remains
response projection. `oauthCompleteFromUri` and `oauthWithBrowser` also forward
profile input, including explicit-realm helpers. Existing cancellation arguments
are unchanged. The server validates schema fields and create rules only when
creating a new account; required custom fields still apply to omitted input.
Nonempty profiles on existing OAuth login/link are rejected, not merged into an
existing account. Session responses stay unchanged; read custom attributes with
the realm's `profile()` method.

Use the explicit auth realm to discover methods and refresh a session:

```dart
final members = client.realm('members');
final methods = await members.methods();
if (methods.otpEnabled) await members.requestOtp('person@example.com');
final refreshed = await members.refresh(); // AuthStore now holds refreshed.token
```

Refresh tokens are single-use. The SDK writes a replacement only after it has
validated the complete response. If a refresh response is lost after the server
rotates the token, the old token may already be invalid and the replacement
cannot be recovered; clear local auth and have the user sign in again instead
of retrying with the old token.

See `../../../docs/dart-sdk.md` for examples and test commands.
