# 10-minute quickstart

This path runs the development Compose stack: PostgreSQL 16.13 plus the locally built pgBase image. It requires Docker Engine or Docker Desktop with Docker Compose v2. The local ports `8090` and `5432` must be available unless you change them in `deployments/.env`.

## 1. Create the local environment file

The stack requires a PostgreSQL password, a setup token of at least 32 characters, and a base64 token secret that decodes to at least 32 bytes. Never commit `deployments/.env` or reuse these development values in production.

POSIX shell with OpenSSL:

```sh
umask 077
cp deployments/.env.example deployments/.env
POSTGRES_PASSWORD="$(openssl rand -hex 32)"
SETUP_TOKEN="$(openssl rand -hex 32)"
TOKEN_SECRET="$(openssl rand -base64 32 | tr -d '\n')"
sed -i.bak \
  -e "s|^PGBASE_POSTGRES_PASSWORD=.*|PGBASE_POSTGRES_PASSWORD=$POSTGRES_PASSWORD|" \
  -e "s|^PGBASE_SETUP_TOKEN=.*|PGBASE_SETUP_TOKEN=$SETUP_TOKEN|" \
  -e "s|^PGBASE_TOKEN_SECRET=.*|PGBASE_TOKEN_SECRET=$TOKEN_SECRET|" \
  deployments/.env
rm deployments/.env.bak
unset POSTGRES_PASSWORD SETUP_TOKEN TOKEN_SECRET
```

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

The current record API is protected by the administrator session; this is not public user authentication. Log in with curl, capture the cookie, and copy the `csrfToken` value from the JSON response:

```sh
curl --fail-with-body \
  --cookie-jar pgbase.cookies \
  --header 'Content-Type: application/json' \
  --header 'Origin: http://127.0.0.1:8090' \
  --data '{"email":"admin@example.com","password":"replace-with-your-password"}' \
  http://127.0.0.1:8090/api/admin/login

CSRF_TOKEN='paste-the-csrfToken-response-value-here'

curl --fail-with-body \
  --cookie pgbase.cookies \
  --header 'Content-Type: application/json' \
  --header 'Origin: http://127.0.0.1:8090' \
  --header "X-PgBase-CSRF: $CSRF_TOKEN" \
  --data '{"title":"hello from pgBase"}' \
  http://127.0.0.1:8090/api/v1/collections/notes/records

curl --fail-with-body \
  --cookie pgbase.cookies \
  http://127.0.0.1:8090/api/v1/collections/notes/records
```

Replace the email and password with the administrator created in the browser. Delete `pgbase.cookies` when finished; it contains an active administrator session.

To stop containers without deleting data, run `docker compose --env-file deployments/.env -f deployments/docker-compose.yml down`. Adding `--volumes` permanently deletes this local PostgreSQL data.
