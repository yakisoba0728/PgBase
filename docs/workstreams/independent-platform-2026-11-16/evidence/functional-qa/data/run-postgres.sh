#!/usr/bin/env bash
# Execute the command in the same process/network namespace as its disposable DB.
# PGBASE_QA_PG_BIN must name the directory containing initdb, pg_ctl, and psql.
set -euo pipefail
: "${PGBASE_QA_PG_BIN:?Set PGBASE_QA_PG_BIN to a PostgreSQL bin directory}"
pgbase_qa_runtime=$(mktemp -d /tmp/pgbase-functional-data.XXXXXX)
pgbase_qa_port=${PGBASE_QA_PORT:-56592}
pgbase_qa_started=false
cleanup() {
	pgbase_qa_status=$?
	if "$pgbase_qa_started"; then
		"$PGBASE_QA_PG_BIN/pg_ctl" -D "$pgbase_qa_runtime/data" -m immediate -w stop > "$pgbase_qa_runtime/stop.log" 2>&1 || true
	fi
	if [[ $pgbase_qa_status == 0 ]]; then
		rm -rf -- "$pgbase_qa_runtime"
	else
		printf 'Disposable fixture retained at %s\n' "$pgbase_qa_runtime" >&2
	fi
}
trap cleanup EXIT
"$PGBASE_QA_PG_BIN/initdb" -D "$pgbase_qa_runtime/data" -U postgres -A trust --no-locale --encoding=UTF8 --no-sync > "$pgbase_qa_runtime/initdb.log" 2>&1
printf "\nunix_socket_directories = ''\n" >> "$pgbase_qa_runtime/data/postgresql.conf"
"$PGBASE_QA_PG_BIN/pg_ctl" -D "$pgbase_qa_runtime/data" -l "$pgbase_qa_runtime/postgres.log" -o "-h 127.0.0.1 -p $pgbase_qa_port" -w start > "$pgbase_qa_runtime/start.log" 2>&1
pgbase_qa_started=true
export PGBASE_TEST_ADMIN_URL="postgres://postgres@127.0.0.1:$pgbase_qa_port/postgres?sslmode=disable"
"$PGBASE_QA_PG_BIN/psql" "$PGBASE_TEST_ADMIN_URL" -XAtc 'select version();'
"$@"
