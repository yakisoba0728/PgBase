# Public API additions

The source branch implements the contracts below. Use `Content-Type: application/json` for JSON writes. User routes use `Authorization: Bearer <session-token>`; administrative writes use the administrator session cookie, same-origin validation and `X-PgBase-CSRF`. Service tokens use their own route namespace. User routes never accept an administrator cookie as user identity.

## Authentication

| Method and route | JSON request / result |
| --- | --- |
| POST `/api/v1/auth/register` or `/login` | `{email,password}` → `{token,user,expiresAt}` |
| GET `/api/v1/auth/session` | Current bearer identity; any auth collection |
| POST `/api/v1/auth/logout` | Revoke current bearer session |
| POST `/api/v1/auth/change-password` | `{currentPassword,newPassword}`; revokes sessions and pending MFA challenges |
| POST `/api/v1/auth/request-verification` | Authenticated request, `{}` |
| POST `/api/v1/auth/confirm-verification` | `{token}` |
| POST `/api/v1/auth/request-password-reset` | `{email}`; same response for unknown users |
| POST `/api/v1/auth/confirm-password-reset` | `{token,newPassword}` |
| POST `/api/v1/auth/request-email-change` | `{currentPassword,newEmail}` with bearer |
| POST `/api/v1/auth/confirm-email-change` | `{token}` |
| POST `/api/v1/auth/totp/enroll` | `{currentPassword}` → secret/enrollment URI |
| POST `/api/v1/auth/totp/confirm` | `{code}` → one-time recovery codes |
| POST `/api/v1/auth/totp/disable` | `{currentPassword,code}` |
| POST `/api/v1/auth/mfa/complete` | `{challengeToken,code}` → user session |
| POST `/api/v1/auth/oauth/start` | `{provider,redirectUri}` → authorization URL and expiry |
| POST `/api/v1/auth/oauth/link` | `{currentPassword,provider,redirectUri}` with bearer |
| POST `/api/v1/auth/oauth/complete` | `{provider,state,code}` → user session |
| POST `/api/v1/auth/otp/request` | `{email}` → opaque OTP id and expiry without disclosing account existence |
| POST `/api/v1/auth/otp/complete` | `{otpId,code}` → user session or MFA challenge |

The TypeScript SDK accepts a browser callback directly with `realm.oauthCompleteFromURL(provider, window.location.href)`. It reports provider cancellation and callback errors as `OAuthCallbackError` without sending an incomplete callback to the server. OAuth providers that do not return a verified email can only be used after the authenticated, current-password account-link flow; PgBase never invents an email or merges by an unverified address.

Apple uses the configured client secret as Apple's signed client-secret JWT. Rotate it before its expiry. The provider's user-info URL is the Apple JWKS endpoint (`https://appleid.apple.com/auth/keys`); PgBase verifies RS256 signatures and the issuer, audience, expiry, issued-at, not-before, and PKCE-bound nonce claims before accepting an identity. Apple returns scoped callbacks with `form_post`. PgBase sends that response through its public `/api/v1/auth/oauth/apple/callback` bridge, atomically validates the state, and redirects once to the registered application callback. Browser code can then call `oauthCompleteFromURL` just like other providers.

The built-in profile catalog matches the 30 providers in the vendored PocketBase v0.40.3 source: Apple, Bitbucket, Box, Discord, Facebook, Gitea/Forgejo, Gitee, GitHub, GitLab, Google, Instagram, Kakao, Lark, Linear, LiveChat, Mailcow, Microsoft, monday.com, Notion, OIDC, Patreon, Planning Center, Spotify, Strava, Trakt, Twitch, X/Twitter, VK, WakaTime, and Yandex. Provider URLs stay editable for regional or self-hosted services. Apple production configuration requires a public HTTPS PgBase URL, and its Services ID redirect URI must be the HTTPS callback bridge URL shown above.

A login requiring MFA returns HTTP 401 with `mfa_required` and challenge fields in `error.details`. The TypeScript SDK exposes `MFAChallengeError`. OAuth uses configured providers and allowlisted redirect URIs; callback completion consumes state, validates PKCE through the token exchange and preserves the auth collection stored at initiation. Matching email alone never links an existing account. Email action links use a URL fragment and an explicit confirmation page at `/auth/action`.

New OAuth registrations may import a provider avatar when `avatarField` contains an image URL. Imports are optional and bounded to 2 MiB, use public HTTPS URLs (or explicitly enabled loopback fixtures), validate media type and dimensions, and never replace an existing managed avatar. An authenticated user reads their private avatar at `GET /api/v1/auth/avatar`.

Named auth collections use `/api/v1/auth/{authCollection}/register`, `/login`, `/session`, `/logout`, `/request-password-reset` and `/oauth/start`. Named session/logout/profile routes reject a token from a different collection. The generic session/logout routes operate on the presented bearer identity. GET/PATCH `/api/v1/auth/{authCollection}/profile` returns `{attributes:...}` and PATCH accepts the profile attribute object. The SDK's `realm(name).profile()` and `updateProfile()` unwrap that response.

Admin auth-collection management is GET/POST `/api/admin/auth-collections` and PATCH/DELETE `/api/admin/auth-collections/{id}`. Creation takes `{id,name,registrationEnabled,profileSchema}`. PATCH replaces settings and requires both `registrationEnabled` and `profileSchema`; omission is rejected. Profiles are typed scalar/JSON fields. There are at most 100 auth collections, including `users`; the default cannot be deleted.

Administrator user lifecycle routes are GET `/api/admin/users?after={uuid}&limit=1..100`, PATCH `/api/admin/users/{id}` with `{active}`, POST `/api/admin/users/{id}/revoke-sessions`, and POST `/api/admin/users/{id}/impersonate` with an optional `{durationSeconds}` from 60 through 900. The list result is `{items:[{id,email,verified,active,createdAt,updatedAt}],nextAfter?}`. Deactivation and explicit revocation also invalidate pending MFA challenges; activation does not restore old sessions.

Impersonation returns a temporary user bearer session with `impersonated:true` and `impersonatingAdministratorId`. It expires after five minutes by default, never later than the administrator session that created it, and stops authenticating when that administrator session is revoked or its administrator is deactivated. Impersonated sessions carry no MFA proof and cannot change passwords, verification state, email, TOTP, or OAuth links. Custom Go routes receive the same origin fields through `CustomPrincipalFromContext`, so application-specific sensitive actions can reject impersonation explicitly.

## Records and permissions

User CRUD is `/api/v1/user/collections/{collection}/records[/{id}]`. Anonymous and service-scoped variants use `/api/v1/public/collections/...` and `/api/v1/service/collections/...`. Collection opt-in and typed action rules govern user/anonymous access. Service tokens carry explicit collection/action grants. Target view permission applies to relation IDs, filters, sorting and expansion; cascade writes require each affected target's write permission. Rules apply before pagination. A changed policy invalidates dependent cursors and subscription delivery.

List query parameters are `filter`, `sort`, `expand`, `cursor` and `limit`. API system fields use `created_at`, `updated_at` and `id`; they are not camel-cased sort identifiers. Records and mutation responses carry strong ETag headers. Use `If-Match` for update/delete and a stable `Idempotency-Key` for create retries. The SDK's `getWithETag`, `createWithETag` and `updateWithETag` return `{record,etag}`.

Read-only views are created through the existing schema plan/apply endpoints. A `view` may contain a typed source collection, projection, and static filter, or one bounded `sql` SELECT whose output fields and collection dependencies PostgreSQL infers during preview. Declarative view reads use source admission, ownership, and action rules even when a rule field is not projected. SQL views apply their own collection policy. Views reject create, update, delete, and relation-target use.

Batch POST `/api/v1/user/collections/batch` accepts `{operations:[...]}`: tagged create/update/delete entries with `collection`, `input` where applicable, `id`, `ifMatch`, and `idempotencyKey`. The limit is 50 operations and a 1 MiB request. All operations commit or roll back together; results are `{eventId,record?,etag?,version?}`. The corresponding routes are `/api/v1/collections/batch` for administrators, `/api/v1/service/collections/batch` for service tokens and `/api/v1/public/collections/batch` for anonymous access.

The same endpoint also accepts the generic `{requests:[{method,url,body,headers}]}` envelope. Nested URLs are limited to record POST, PATCH, PUT upsert, and DELETE paths under `/api/v1/collections/{name}/records`; `/api/admin/...` is also accepted for administrator batches. Nested authorization and cookie headers are rejected because every request uses the authenticated parent principal. Generic results contain the nested `status`, `headers.ETag`, and `body`.

PUT creates the requested UUID when it is absent and otherwise applies the normal update contract. Repeating a PUT converges to the same record state; an update to an existing record still creates a new version, ETag, audit event, and outbox event, including when the submitted values already match.

For atomic file attachment, send `multipart/form-data` with one JSON part named `requests` and uniquely named file parts. Reference a part from a record body field as `{"$file":"partName"}` or use an array of those objects for a multi-file field. Every file part must be referenced exactly once. Bytes are staged before the PostgreSQL transaction, attached only after all record operations succeed, and queued for cleanup on rollback. The multipart request is bounded to 100 MiB and 50 operations; field-specific file size, MIME, count, and required rules still apply.

Admin GET/PUT `/api/admin/collections/{collection}/rules` uses `{rules:{list,view,create,update,delete}}`. The bounded typed AST supports `allow`, `deny`, `owner`, `authenticated`, boolean groups, typed comparisons and selected auth references. It accepts no SQL or scripts. POST `/rules/preview` accepts `{rule,action,userId?,recordIds}` for 1–50 existing IDs and returns `{items:[{recordId,allowed}]}` without saving the rule. Omitted user ID previews anonymous access. Create validation is exercised through actual record mutation tests, not this existing-row preview.

## Files and events

File fields are virtual metadata fields. `options.maxSelect` accepts 1–100 files and defaults to one. The field's `required` flag is the minimum contract: a required field must retain at least one file; file fields do not accept a separate `minSelect` option. GET/POST `/api/v1/user/collections/{collection}/records/{id}/files/{field}` lists/uploads. Upload has exactly one multipart part named `file`, plus `X-PgBase-File-Size` and `X-PgBase-File-SHA256`. Files must be attached to an existing record. POST `/api/v1/user/files/{fileId}/token` takes `{variant:"original"|"thumbnail"}`. GET `/api/v1/user/files/{fileId}?token=...&variant=...` requires both the bound bearer and the download token; possession of the URL alone grants nothing. DELETE uses the same file path. Admin equivalents start `/api/admin`.

SSE subscription is GET `/api/v1/user/collections/{collection}/events`. See [realtime](realtime.md) for replay IDs, retention, failure handling and limits.

Admin service-token issuance takes `{name,ttlSeconds,scopes:[{collectionId,action}]}` at POST `/api/admin/service-tokens`. Actions are `read`, `create`, `update`, `delete`; the returned secret is shown only on issuance. GET lists metadata. POST `/api/admin/service-tokens/{id}/revoke` revokes it. Store tokens only in trusted server environments.

## Compatibility and errors

These source additions are pre-release; the locally packable SDK is `0.1.0-local` and is private. The existing `v0.1.0` release is unchanged. Consumers should pin source/package versions together until a compatible release is published. Additive fields may appear; clients should ignore unknown response fields. Removing a supported public route or changing its semantics requires an explicit documented release change, compatibility tests and a migration path; no deprecation is announced here.

Errors use `{error:{code,message,details?},requestId?...}` according to the common HTTP envelope. Validation generally returns 400, invalid credentials 401, denied collection admission 403, hidden/missing records 404, mutation conflicts 409/412, oversized bodies 413, rate/connection limits 429 and unavailable dependencies 503. Inspect the error code rather than matching message prose. Complete path/admission coverage is checked against [the generated OpenAPI route contract](openapi.routes.json); that artifact intentionally does not replace dynamic record schemas.

## Local validation boundary

These additions have been exercised with source builds, disposable PostgreSQL instances, local HTTP clients, a local SMTP sink and local OAuth fixtures. The TypeScript SDK is buildable and packable locally; it is not published as a supported npm release. No real external OAuth account or external mail delivery was used. Cloud deployment behavior, production SLOs, multi-hour soak results and public package or image publication remain unverified and are not claimed by this document.
