# PgBase Dart SDK

`pgbase` is a local Dart and Flutter client for PgBase's versioned HTTP API.
It is intentionally not publishable and does not claim PocketBase SDK
compatibility.

Use a caller-selected `AuthStorage` only when the application has an
appropriate secure persistence mechanism. The default `AuthStore` is memory
only. Administrator sessions use cookies and in-memory CSRF state through a
separate `PgBaseAdminClient`; never pass a user bearer token to it.

See `../../../docs/dart-sdk.md` for examples and test commands.
