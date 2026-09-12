# PgBase Dart SDK

`pgbase` is a local Dart and Flutter client for PgBase's versioned HTTP API.
It is intentionally not publishable and does not claim PocketBase SDK
compatibility.

Use a caller-selected `AuthStorage` only when the application has an
appropriate secure persistence mechanism. The default `AuthStore` is memory
only. Administrator sessions use cookies and in-memory CSRF state through a
separate `PgBaseAdminClient`; never pass a user bearer token to it.

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
