#!/usr/bin/env bash
# Focused source controls, not an HTTP performance qualification.
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
evidence_dir="$repo_root/docs/workstreams/independent-platform-2026-11-16/evidence/final-master/performance-blocker-recovery/agent-b"
: "${PGBASE_RECOVERY_TOOLS:?Set to the directory containing go/ and pgsql17/}"
pg_bin="$PGBASE_RECOVERY_TOOLS/pgsql17/bin"
go_bin="$PGBASE_RECOVERY_TOOLS/go/bin/go"
pg_temp=$(mktemp -d /tmp/pgbase-recovery-agent-b.XXXXXX)
pg_data="$pg_temp/data"
pg_port=55437
pg_log="$evidence_dir/focused-fixture-postgres.log"
cleanup() {
  "$pg_bin/pg_ctl" -D "$pg_data" -m fast -w stop >> "$evidence_dir/focused-fixture-teardown.txt" 2>&1 || true
}
trap cleanup EXIT
"$pg_bin/initdb" -D "$pg_data" --encoding=UTF8 --locale=C --auth-local=trust --auth-host=trust > "$evidence_dir/focused-fixture-initdb.txt" 2>&1
# Unix sockets are unavailable in this executor. A private loopback listener
# and the tests live inside this single command's process/network lifetime.
"$pg_bin/pg_ctl" -D "$pg_data" -l "$pg_log" -o "-h 127.0.0.1 -k '' -p $pg_port" -w start > "$evidence_dir/focused-fixture-start.txt" 2>&1
export PGBASE_TEST_ADMIN_URL="postgres://$(id -un)@127.0.0.1:$pg_port/postgres?sslmode=disable"
export GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4
"$pg_bin/psql" "$PGBASE_TEST_ADMIN_URL" -At -c "select name,setting from pg_settings where name in ('server_version','fsync','synchronous_commit','full_page_writes','max_connections','shared_buffers') order by name" > "$evidence_dir/focused-postgresql-settings-before.txt"
"$go_bin" version > "$evidence_dir/focused-go-version.txt"
git rev-parse HEAD 'HEAD^{tree}' > "$evidence_dir/focused-source.txt"
set +e
"$go_bin" test -json -race -tags=integration -p 1 -count=1 -timeout=3m \
  -run '^(TestInTxRollsBackOnError|TestInTxRollsBackOnPanic|TestInTxExpiredDeadlineRollsBackAndReleasesConnection|TestPinnedRecordCatalogBlocksSchemaDDLUntilTheRecordTransactionEnds|TestListenerPreservesRequestPoolCapacityAcrossReconnectAndShutdown|TestReadKernelQueryCountIsBoundedByPlanShapeInsteadOfRootCount)$' \
  ./internal/database ./internal/schema ./internal/query \
  > "$evidence_dir/focused-controls-race.jsonl" 2> "$evidence_dir/focused-controls-race.stderr.txt"
test_result=$?
set -e
printf '%s\n' "$test_result" > "$evidence_dir/focused-controls-exit-code.txt"
"$pg_bin/psql" "$PGBASE_TEST_ADMIN_URL" -At -c "select name,setting from pg_settings where name in ('server_version','fsync','synchronous_commit','full_page_writes','max_connections','shared_buffers') order by name" > "$evidence_dir/focused-postgresql-settings-after.txt"
"$pg_bin/psql" "$PGBASE_TEST_ADMIN_URL" -At -c "select datname from pg_database where datname like 'pgbase_test_%' order by datname" > "$evidence_dir/focused-database-cleanup.txt"
exit "$test_result"
