# SDK authentication cookies

The TypeScript and Dart SDKs use the same SSR cookie policy. Cookies carry a cached opaque token and optional cached identity. They are **not signed identity assertions**: validate the token with PgBase before making authorization decisions. `AuthStore.isValid` only reports that a non-empty token is present.

## Current contract and breaking changes

The default cookie name is `pgbase_auth`, replacing `pb_auth`. Loading does not silently fall back to another product's cookie namespace. An application deliberately retaining an old name must pass `key: "pb_auth"` on both export and import. This change does not migrate, read or delete production cookies.

Default attributes are `Path=/; HttpOnly; SameSite=Lax`. `Secure` is explicit to support local HTTP development; **set `secure: true` on HTTPS deployments**. Both SDKs accept `httpOnly`, `secure`, `sameSite` and `path`. The Dart SDK previously exposed only name and path.

`SameSite=None` requires `Secure`; unsupported SameSite values are rejected rather than interpolated into a header. Cookie names reject all non-token characters, including trailing control characters. Paths must be absolute printable ASCII without semicolons. Protected cookie-name prefixes require their associated Secure, HttpOnly and root-path attributes. Domain attributes are intentionally not provided.

TypeScript:

```ts
const cookie = client.auth.exportToCookie({ secure: true, sameSite: "Lax" });
// Write cookie to Set-Cookie in the server response.
const restored = new AuthStore();
restored.loadFromCookie(request.headers.get("cookie") ?? "");
// Validate restored.token with PgBase before trusting any cached record.
```

Dart:

```dart
final cookie = client.auth.exportToCookie(secure: true, sameSite: 'Lax');
final restored = AuthStore();
await restored.loadFromCookie(requestCookieHeader);
// Validate restored.token; dispose restored with close() when finished.
```

A malformed matching cookie clears cached state. A missing cookie does not mutate existing state. Use a fresh AuthStore per SSR request: never share a mutable user store across unrelated requests. Cookie attributes do not replace origin/CSRF protections, token expiry/revocation or server-side authorization. Exporting a null token is not an HTTP deletion directive; applications that remove cookies must send their own expiry/max-age instruction with the same name/path.

Policy references: [RFC 6265 cookie syntax](https://httpwg.org/specs/rfc6265.html) and [MDN Set-Cookie attributes and prefixes](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie). Neither the SDK helper nor its tests claim universal browser enforcement of cookie prefixes.
