# Public users and owner-only records

This feature is in the source after v0.1.0. The published v0.1.0 image does not contain these endpoints. Build the current source using the [development instructions](deployment.md), or use a later release that explicitly includes them. Existing installations must follow the migration 9→11 procedure in [upgrades](upgrades.md).

## Enable a collection

Set `PGBASE_USER_REGISTRATION=true` before starting the server. For Compose, add it to `deployments/.env` and recreate the pgBase service. The default is `false`; disabling registration leaves existing users able to log in.

Follow the [quickstart](quickstart.md) to create a scalar `notes` collection with a text `title` field and obtain the administrator cookie and protected request headers. Before cleaning up those request files, enable owner access:

```sh
curl --fail-with-body --request PUT \
  --cookie "$pgbase_request_dir/cookies" \
  --header "@$pgbase_request_dir/request.headers" \
  --data '{"enabled":true}' \
  http://127.0.0.1:8090/api/admin/collections/notes/user-access
```

`GET` on the same URL reads the setting. `PUT {"enabled":false}` blocks subsequent user operations. Both routes require an administrator session; mutation uses the existing Origin and CSRF requirements.

The setting defaults to disabled. It is a stored opt-in, not a guarantee that the collection is usable by public users: any outgoing or incoming relation blocks public access even if `enabled` is true. Adding a relation to a previously usable collection blocks subsequent public operations. Administrator record operations retain their existing behavior.

## Register, log in, and use records

Use a separate directory for public-user credentials. Write your email and password to `credentials.json` with an editor, using this shape: `{"email":"alice@example.com","password":"your-own-long-passphrase"}`. Passwords must contain 12–1024 bytes.

```sh
umask 077
pgbase_user_dir="$(mktemp -d)"
${EDITOR:-vi} "$pgbase_user_dir/credentials.json"
curl --fail-with-body \
  --header 'Content-Type: application/json' \
  --data-binary "@$pgbase_user_dir/credentials.json" \
  --output "$pgbase_user_dir/session.json" \
  http://127.0.0.1:8090/api/v1/auth/register
jq --exit-status --raw-output \
  '["Content-Type: application/json", "Authorization: Bearer " + .token][]' \
  "$pgbase_user_dir/session.json" > "$pgbase_user_dir/headers"

curl --fail-with-body --header "@$pgbase_user_dir/headers" \
  --header 'Idempotency-Key: first-note-00001' \
  --data '{"title":"my private note"}' \
  --dump-header "$pgbase_user_dir/record.headers" \
  --output "$pgbase_user_dir/record.json" \
  http://127.0.0.1:8090/api/v1/user/collections/notes/records

curl --fail-with-body --header "@$pgbase_user_dir/headers" \
  http://127.0.0.1:8090/api/v1/user/collections/notes/records
```

Registration returns HTTP 201 with `{token,user:{id,email,createdAt},expiresAt}`. Log in later by sending the same credentials shape to `POST /api/v1/auth/login`; it returns HTTP 200 with a new session. Sessions last 24 hours. Store the token as a credential. The database stores only its keyed hash, and session inspection does not echo it. Named realm sessions can be refreshed before expiry: refresh rotates the opaque token in the same revocable session row and renews its 24-hour sliding expiry. It has no absolute lifetime. Save the returned replacement before discarding it; if its response is lost, do not retry the old bearer token—clear it and sign in again.

| Operation | Method and path | Contract |
| --- | --- | --- |
| Inspect session | `GET /api/v1/auth/session` | Bearer token; user and expiry |
| Refresh a realm session | `POST /api/v1/auth/{authCollection}/refresh` | Bearer token; atomically replaces it and renews the sliding 24-hour expiry |
| Discover realm methods | `GET /api/v1/auth/{authCollection}/methods` | Public password/OTP/MFA flags and enabled OAuth provider names only |
| Log out | `POST /api/v1/auth/logout` | Bearer token; 204, token revoked |
| Change password | `POST /api/v1/auth/change-password` | Current password required; 204, all sessions revoked; [details](user-management.md) |
| List / create | `GET` / `POST /api/v1/user/collections/{collection}/records` | Own records only |
| Read / update / delete | `GET` / `PATCH` / `DELETE /api/v1/user/collections/{collection}/records/{id}` | Own record only; send its current `If-Match` ETag for conditional updates/deletes |

Read the record's `id` from the create response and its `ETag` from the response headers. A PATCH sends a partial record JSON object and that ETag; save the new ETag before another mutation. DELETE uses the latest ETag. Omitting `If-Match` retains the existing last-write-wins compatibility behavior; clients should send it to detect stale updates. Create retries may reuse `Idempotency-Key` (16–128 bytes) with the same request. The key is scoped to the user, and replay still checks current access. Listing retains the existing filter, sort and cursor contract; a cursor cannot be reused by another user.

Register a second email in a separate protected directory and list `notes` using its token. Its list will be empty. Direct GET/PATCH/DELETE of the first user's record returns the same 404 as a missing record. Each user can create and manage their own records. There is no client-settable owner field, and existing administrator-created records remain unowned and invisible through this namespace.

```sh
curl --fail-with-body --request POST --header "@$pgbase_user_dir/headers" \
  http://127.0.0.1:8090/api/v1/auth/logout
rm -f "$pgbase_user_dir/credentials.json" "$pgbase_user_dir/session.json" \
  "$pgbase_user_dir/headers" "$pgbase_user_dir/record.headers" "$pgbase_user_dir/record.json"
rmdir "$pgbase_user_dir"
unset pgbase_user_dir
```

## Current boundaries

Administrator cookies do not authenticate public-user routes. Public Bearer tokens do not authenticate administrator routes. Credential and authenticated user responses use `Cache-Control: no-store`. Browser applications use the configured CORS origins and Bearer headers.

Unknown accounts and wrong passwords both return 401. Login limiting is shared in PostgreSQL: five account failures or twenty network failures trigger a 15-minute block. Correct credentials remain blocked during that interval; successful login does not erase prior failures. Registration has its own network limit. A 429 response includes `Retry-After`; bounded concurrent admission can also return 429.

Project users, configurable auth collections, password/OAuth/OTP/MFA flows, password reset, verification, relation-aware access, files, realtime and local client SDKs are implemented in the current source. Their PocketBase wire compatibility remains intentionally incomplete; see the [detailed parity audit](pocketbase-parity-detailed-audit-2026-09-10.md).

Backups must include `pgbase_meta` along with application schemas. The PostgreSQL 16/17/18 restore drill exercises restored users, sessions, ownership and cross-user isolation. Keep the same token secret when restoring an installation if existing sessions must remain usable.
