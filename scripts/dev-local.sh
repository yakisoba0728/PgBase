#!/usr/bin/env bash
set -euo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"
umask 077
dev_dir="$root_dir/.local/pgbase"
mkdir -p "$dev_dir/bin" "$dev_dir/mail" "$dev_dir/files"
if [[ ! -f "$dev_dir/environment" ]]; then
  db_password="$(openssl rand -hex 24)"
  setup_token="$(openssl rand -hex 32)"
  token_secret="$(openssl rand -base64 32)"
  {
    printf 'export PGBASE_LOCAL_DATABASE_PASSWORD=%q\n' "$db_password"
    printf 'export PGBASE_DATABASE_URL=%q\n' "postgres://pgbase:$db_password@127.0.0.1:55440/pgbase?sslmode=disable"
    printf 'export PGBASE_SETUP_TOKEN=%q\n' "$setup_token"
    printf 'export PGBASE_TOKEN_SECRET=%q\n' "$token_secret"
    printf 'export PGBASE_FILES_LOCAL_DIRECTORY=%q\n' "$dev_dir/files"
    cat <<'ENV'
export PGBASE_HTTP_ADDRESS=127.0.0.1:8090
export PGBASE_PUBLIC_URL=http://127.0.0.1:8090
export PGBASE_AUTO_MIGRATE=true
export PGBASE_USER_REGISTRATION=true
export PGBASE_SMTP_ADDRESS=127.0.0.1:1025
export PGBASE_SMTP_FROM=local@pgbase.test
export PGBASE_SMTP_ALLOW_INSECURE=true
export PGBASE_FILES_DRIVER=local
export PGBASE_CORS_ALLOWED_ORIGINS=http://127.0.0.1:5173
ENV
  } > "$dev_dir/environment"
fi
# This file is generated locally with shell-quoted values and never committed.
source "$dev_dir/environment"
docker compose -f deployments/local/docker-compose.yml up -d --wait
GOTOOLCHAIN=go1.26.8 go build -o "$dev_dir/bin/devmail" ./tools/devmail
GOTOOLCHAIN=go1.26.8 go build -o "$dev_dir/bin/pgbase" ./cmd/pgbase
"$dev_dir/bin/devmail" --dir "$dev_dir/mail" > "$dev_dir/mail.log" 2>&1 &
mail_pid=$!
trap 'kill "$mail_pid" 2>/dev/null || true; wait "$mail_pid" 2>/dev/null || true' EXIT
printf 'Admin: http://127.0.0.1:8090/admin\nInbox: http://127.0.0.1:8025\nLocal settings and setup token: %s/environment\n' "$dev_dir"
"$dev_dir/bin/pgbase" serve
