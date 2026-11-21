# Managing public users

This feature is available in source after M1 (PR #26). It is not part of the published v0.1.0 image. It uses the existing migration-11 user/session tables and needs no additional migration. An installation still on v0.1.0 must first follow the [9→11 upgrade procedure](upgrades.md).

## Administrator UI

Sign in to the administrator UI and open **Users**. The page shows public users' email, auth collection, ID, active state and timestamps, with the number of matching users (shown as "more than 10000" beyond that). It loads up to 100 users per page in ascending ID order; **Load more** continues and **Refresh** returns to the first page. Search takes a literal email substring or exact user ID and the collection selector narrows the list; both run on the server. This is a live paginated view, so refresh to discover new accounts created before the current page boundary.

- **Deactivate** confirms the action, makes the account inactive and revokes all its stored sessions in one transaction. The user's existing records and ownership remain intact.
- **Activate** permits fresh login. It never restores previously revoked sessions.
- **Revoke sessions** confirms the action and revokes every previously unrevoked stored session for that user. The reported count includes expired rows that were not already marked revoked. An active user can log in again immediately.

Controls are disabled during a mutation so a second action cannot overlap it. Failed operations leave an error and permit retry. Navigation or administrator-session replacement prevents old request completions from changing the new page.

The **Collections** page also exposes **Enable user access** / **Disable user access** for scalar collections. A failed policy lookup offers **Retry user access**. Collections with incoming or outgoing relations show that public access is blocked. The stored server policy and its runtime relation restriction remain described in [public user access](user-access.md).

## Administrator API

These routes use the administrator cookie. Reads enforce the existing same-origin rule; mutations also require `X-PgBase-CSRF` and the configured Origin. Public Bearer tokens cannot access them.

| Method and route | Request | Success response |
| --- | --- | --- |
| `GET /api/admin/users` | Optional `after=<UUID>`, `limit=1..100` (default 50), `authCollectionId=<UUID>`, `q=<email substring or exact user id>`, `total=true` | `200 {items:[user],nextAfter?:UUID,total?:number,totalCapped?:true}` |
| `GET /api/admin/users/{id}` | No body | `200 user` |
| `PATCH /api/admin/users/{id}` | JSON `{active:boolean}` | `200 user` |
| `POST /api/admin/users/{id}/revoke-sessions` | No body | `200 {revokedSessions:number}` |
| `POST /api/admin/users/{id}/impersonate` | `{durationSeconds?:60..900}` | Temporary user session with explicit administrator origin |

The `user` shape is `{id,authCollectionId,email,verified,active,createdAt,updatedAt}`.

Filters combine with the `after` keyset: `authCollectionId` uses `(auth_collection_id,id)`; `q` matches an exact UUID or a case-insensitive literal email substring. `%`, `_` and backslash are literal characters. Schema 66 adds a pg_trgm GIN index for email searches; fewer than three searchable characters can still require a scan. `total=true` adds the number of matching users; it counts at most 10,000 and then reports `total:10000,totalCapped:true`. An unknown filter value, an empty parameter or `q` longer than 320 bytes returns 400. It never includes password hashes, session tokens or rate-limit data. Empty lists return `items:[]`; the last page omits `nextAfter`. Unknown/duplicate query fields, invalid IDs and malformed bodies return 400. A missing target user returns 404. Database failures return a redacted 503. Responses use `Cache-Control: no-store`.

## Change your password

A public user sends their current Bearer token to:

```http
POST /api/v1/auth/change-password
Authorization: Bearer <current user token>
Content-Type: application/json

{"currentPassword":"your-current-passphrase","newPassword":"your-new-passphrase"}
```

The new password follows the existing 12–1024-byte password limits. On success the server returns **204** and atomically revokes **all sessions, including the token used for this request**. Discard stored tokens and log in again with the new password. No replacement token is returned.

A wrong current password returns 401 and leaves the stored password and sessions intact. Password-change failures have a shared account limit: the fifth failure returns 429 and blocks further attempts for 15 minutes, including requests with correct credentials. Invalid input returns 400; internal failures return a redacted 503. This is a password-change flow requiring an authenticated session and the current password.

Credential verification and hashing run before taking the user-row lock. The transaction then rechecks active state and the exact password hash before changing credentials and revoking sessions. A concurrent password change cannot overwrite credentials checked before the other change. Login and administrator lifecycle operations use the same user-row lock boundary: sessions committed before revocation are invalidated; a fresh login serialized afterward may issue a new session for an active account. Already-running record requests may finish before deactivation obtains its lock; subsequent requests are denied.

## Email verification, address changes, and recovery

When email actions are configured, authenticated users can request verification at `POST /api/v1/auth/request-verification` and an address change at `POST /api/v1/auth/request-email-change`. An address change also requires the current password. The following delivery limits apply independently of optional HTTP rate limits and are shared by all server instances using the database:

| Limit | Scope |
| --- | --- |
| 5 accepted delivery requests per 15-minute window | Each user, combining verification and address-change requests even when the destination changes |
| 5 accepted delivery requests per 15-minute window | Each destination address, purpose, and auth collection, combining requests from all users in that collection |
| 60-second resend cooldown | Each user and purpose, and each destination address, purpose, and auth collection |

Because destination limits are separate per purpose and auth collection, address-change requests and accounts in other auth collections do not consume the verification capacity of the address owner. One address can therefore receive up to 5 messages per 15-minute window for each purpose and auth collection. Accounts in the same auth collection can still fill the address-change capacity of an address by requesting changes to it; that limit protects the mailbox owner from repeated mail.

Exceeding a delivery limit returns **429** without creating a token or attempting mail delivery. A new accepted verification or address-change request replaces the previous pending action for that user and purpose; use the newest link. Delivery failures do not restore the reserved capacity because the mail server may already have accepted the message. Wait for the cooldown before requesting another link. Explicit administrator-initiated mail remains a trusted operator action and does not use these public-user delivery quotas.

Password-reset requests retain their separate account and network request limits and their nondisclosing response when the address is unknown or delivery fails. Reset confirmation checks token eligibility before password hashing, then rechecks and consumes the token atomically with the password change and session revocation. If the account's email has never been verified, the same transaction also removes its linked OAuth identities, pending OAuth links and TOTP factor, recovery codes and MFA challenges, and enables password sign-in: the reset link reached only that mailbox, so factors added before the owner proved control of it are not trusted. Reset does not mark the account verified, so this repeats on each reset until the email is verified; verified accounts keep their links and MFA. Administrator recovery uses the same preflight and final-consumption ordering. Password work does not hold account or token row locks.

The [TODO](../TODO.md) tracks remaining auth compatibility work and subsequent releases.
