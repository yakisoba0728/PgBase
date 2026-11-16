#!/usr/bin/env bash
set -euo pipefail
pgbase_pg_bin=/workspace/scratch/2a97293516a0/toolchains/postgres/bin
pgbase_go_bin=/workspace/scratch/2a97293516a0/toolchains/go/bin/go
pgbase_cluster=$(mktemp -d /tmp/pgbase-auth-qa.XXXXXX)
pgbase_port=$(python - <<'PY'
import socket
with socket.socket() as s:
    s.bind(('127.0.0.1',0))
    print(s.getsockname()[1])
PY
)
cleanup() {
  "$pgbase_pg_bin/pg_ctl" -D "$pgbase_cluster" -m fast -w stop >/dev/null 2>&1 || true
  rm -rf "$pgbase_cluster"
}
trap cleanup EXIT
"$pgbase_pg_bin/initdb" -D "$pgbase_cluster" -U agent -A trust --no-locale -E UTF8 >/dev/null
"$pgbase_pg_bin/pg_ctl" -D "$pgbase_cluster" -l "$pgbase_cluster/server.log" -o "-h 127.0.0.1 -p $pgbase_port -c unix_socket_directories=''" -w start >/dev/null
export GOTOOLCHAIN=local GOMODCACHE=/tmp/pgbase-functional-modcache GOCACHE=/tmp/pgbase-functional-gocache GOMAXPROCS=2
export PGBASE_TEST_ADMIN_URL="postgres://agent@127.0.0.1:$pgbase_port/postgres?sslmode=disable"
"$pgbase_go_bin" "$@"
