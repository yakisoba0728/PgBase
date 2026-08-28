# 10-minute quickstart

This path runs the development Compose stack: PostgreSQL 16.13 plus the locally built pgBase image. It requires Docker Engine or Docker Desktop with Docker Compose v2. The local ports `8090` and `5432` must be available unless you change them in `deployments/.env`.

## 1. Create the local environment file

The stack requires a PostgreSQL password, a setup token of at least 32 characters, and a base64 token secret that decodes to at least 32 bytes. Never commit `deployments/.env` or reuse these development values in production.

POSIX shell with OpenSSL:

```sh
umask 077
: > deployments/.env
chmod 600 deployments/.env
pgbase_postgres_password="$(openssl rand -hex 32)"
pgbase_setup_token="$(openssl rand -hex 32)"
pgbase_token_secret="$(openssl rand -base64 32 | tr -d '\n')"
{
  printf 'PGBASE_POSTGRES_PASSWORD=%s\n' "$pgbase_postgres_password"
  printf 'PGBASE_SETUP_TOKEN=%s\n' "$pgbase_setup_token"
  printf 'PGBASE_TOKEN_SECRET=%s\n' "$pgbase_token_secret"
  printf '%s\n' \
    'PGBASE_HTTP_PORT=8090' \
    'PGBASE_POSTGRES_PORT=5432' \
    'PGBASE_PUBLIC_URL=http://127.0.0.1:8090' \
    'PGBASE_VERSION=dev' \
    'PGBASE_IMAGE=pgbase:dev' \
    'PGBASE_SHUTDOWN_TIMEOUT=15s'
} > deployments/.env
unset pgbase_postgres_password pgbase_setup_token pgbase_token_secret
```

The POSIX example uses the shell's built-in `printf`, so generated values are written directly to the mode-`600` environment file rather than passed to an external process. Restrict `deployments/.env` to your account with the equivalent filesystem ACL when using PowerShell.

PowerShell:

```powershell
Copy-Item deployments/.env.example deployments/.env
function New-HexSecret {
  $bytes = New-Object byte[] 32
  [Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
  [Convert]::ToHexString($bytes).ToLower()
}
$postgresPassword = New-HexSecret
$setupToken = New-HexSecret
$tokenBytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($tokenBytes)
$tokenSecret = [Convert]::ToBase64String($tokenBytes)
$content = Get-Content -Raw deployments/.env
$content = $content -replace '(?m)^PGBASE_POSTGRES_PASSWORD=.*$', "PGBASE_POSTGRES_PASSWORD=$postgresPassword"
$content = $content -replace '(?m)^PGBASE_SETUP_TOKEN=.*$', "PGBASE_SETUP_TOKEN=$setupToken"
$content = $content -replace '(?m)^PGBASE_TOKEN_SECRET=.*$', "PGBASE_TOKEN_SECRET=$tokenSecret"
Set-Content -NoNewline deployments/.env $content
Remove-Variable postgresPassword, setupToken, tokenBytes, tokenSecret, content
```

Keep the setup token available for the browser setup step. Treat it like a password.

## 2. Start the stack

From the repository root:

```console
docker compose --env-file deployments/.env -f deployments/docker-compose.yml up -d --build --wait
curl --fail-with-body http://127.0.0.1:8090/health/live
curl --fail-with-body http://127.0.0.1:8090/health/ready
```

Both health calls return `{"status":"ok"}`. The development Compose file intentionally sets `PGBASE_AUTO_MIGRATE=true`; do not copy that choice into production.

If startup fails, inspect only the local stack:

```console
docker compose --env-file deployments/.env -f deployments/docker-compose.yml ps
docker compose --env-file deployments/.env -f deployments/docker-compose.yml logs pgbase postgres
```

## 3. Create the first administrator

Open <http://127.0.0.1:8090/admin>. On **Set up pgBase**, enter:

- the `PGBASE_SETUP_TOKEN` stored in `deployments/.env`;
- an administrator email address;
- a password of at least 12 bytes, preferably a longer generated passphrase.

Select **Create administrator**. Setup is one-time: after any administrator exists, the setup endpoint returns `setup_unavailable` even when the original setup token is presented again.

## 4. Create a collection

In the administrator UI:

1. Open **Collections** and select **Create collection**.
2. Use collection name `notes`.
3. Use field name `title`, type `text`, and select `required`.
4. Select **Preview collection**, review the server-generated SQL, then select **Apply collection**.

Names use lowercase letters, digits, and underscores, begin with a lowercase letter, and are at most 48 characters.

## 5. Make the first REST call

The current record API is protected by the administrator session; this is not public user authentication. The following POSIX example requires `curl`, `jq`, and an editor. It keeps the password, session cookie, login response, CSRF header, and request bodies in a private temporary directory instead of placing secret values in process arguments or shell history.

Create the protected files and open the login body in your editor:

```sh
umask 077
pgbase_request_dir="$(mktemp -d)"
chmod 700 "$pgbase_request_dir"
${EDITOR:-vi} "$pgbase_request_dir/login.json"
```

Enter this JSON object in the editor, substituting the administrator created in the browser, then save and close it:

```json
{"email":"admin@example.com","password":"replace-with-your-password"}
```

Log in and have `jq` write the CSRF value directly to curl's protected header file:

```sh
curl --fail-with-body \
  --cookie-jar "$pgbase_request_dir/cookies" \
  --header 'Content-Type: application/json' \
  --header 'Origin: http://127.0.0.1:8090' \
  --data-binary "@$pgbase_request_dir/login.json" \
  --output "$pgbase_request_dir/session.json" \
  http://127.0.0.1:8090/api/admin/auth/login

jq --exit-status --raw-output \
  '["Content-Type: application/json", "Origin: http://127.0.0.1:8090", "X-PgBase-CSRF: " + .csrfToken][]' \
  "$pgbase_request_dir/session.json" > "$pgbase_request_dir/request.headers"
printf '%s\n' '{"title":"hello from pgBase"}' > "$pgbase_request_dir/record.json"

curl --fail-with-body \
  --cookie "$pgbase_request_dir/cookies" \
  --header "@$pgbase_request_dir/request.headers" \
  --data-binary "@$pgbase_request_dir/record.json" \
  http://127.0.0.1:8090/api/v1/collections/notes/records

curl --fail-with-body \
  --cookie "$pgbase_request_dir/cookies" \
  http://127.0.0.1:8090/api/v1/collections/notes/records
```

Delete every protected request file when finished, then remove the empty temporary directory:

```sh
rm -f \
  "$pgbase_request_dir/login.json" \
  "$pgbase_request_dir/session.json" \
  "$pgbase_request_dir/request.headers" \
  "$pgbase_request_dir/record.json" \
  "$pgbase_request_dir/cookies"
rmdir "$pgbase_request_dir"
unset pgbase_request_dir
```

To stop containers without deleting data, run `docker compose --env-file deployments/.env -f deployments/docker-compose.yml down`. Adding `--volumes` permanently deletes this local PostgreSQL data.
