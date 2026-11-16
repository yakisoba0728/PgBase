#!/usr/bin/env bash
set -euo pipefail
pgbase_task_root=/workspace/scratch/27f5e00d0baa
pgbase_fixture_bin="$pgbase_task_root/tools/pgsql17/bin"
pgbase_fixture_data=$(mktemp -d /tmp/pgbase-final-master-XXXXXX)
pgbase_fixture_port=${PGBASE_FIXTURE_PORT:-56847}
pgbase_fixture_log=${PGBASE_FIXTURE_LOG:-"$pgbase_task_root/qa/postgres-fixture-$pgbase_fixture_port.log"}
pgbase_fixture_environment=${PGBASE_FIXTURE_ENVIRONMENT:-"$pgbase_task_root/qa/postgres-fixture-$pgbase_fixture_port-environment.txt"}
cleanup() {
  "$pgbase_fixture_bin/pg_ctl" -D "$pgbase_fixture_data" -m fast -w stop >> "$pgbase_fixture_log" 2>&1 || true
  rm -rf -- "$pgbase_fixture_data"
}
trap cleanup EXIT
"$pgbase_fixture_bin/initdb" -D "$pgbase_fixture_data" -U postgres -A trust --no-locale > "$pgbase_fixture_log" 2>&1
"$pgbase_fixture_bin/pg_ctl" -D "$pgbase_fixture_data" -o "-h 127.0.0.1 -p $pgbase_fixture_port -c unix_socket_directories='' -c shared_buffers=128MB -c max_connections=100" -l "$pgbase_fixture_log" -w start >> "$pgbase_fixture_log" 2>&1
export PGBASE_TEST_ADMIN_URL="postgres://postgres@127.0.0.1:$pgbase_fixture_port/postgres?sslmode=disable"
export PATH="$pgbase_task_root/tools/go/bin:$pgbase_fixture_bin:$pgbase_task_root/tools/dart-sdk/bin:$PATH"
export GOMAXPROCS=${GOMAXPROCS:-2}
"$pgbase_fixture_bin/psql" "$PGBASE_TEST_ADMIN_URL" -XAt -v ON_ERROR_STOP=1 -c "select name,setting from pg_settings where name in ('server_version','fsync','synchronous_commit','full_page_writes','max_connections','shared_buffers') order by name" > "$pgbase_fixture_environment"
"$@"
