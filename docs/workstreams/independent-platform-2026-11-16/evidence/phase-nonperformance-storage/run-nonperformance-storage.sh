#!/usr/bin/env bash
set -euo pipefail
repo=/workspace/scratch/a53362cf26a0/PgBase-storage
pgbin=/workspace/scratch/2a97293516a0/toolchains/postgres/bin
gobin=/workspace/scratch/2a97293516a0/toolchains/go/bin
dbroot=/workspace/scratch/a53362cf26a0/storage-pg
label=$1
shift
cd "$repo"
evidence=docs/workstreams/independent-platform-2026-11-16/evidence/phase-nonperformance-storage
mkdir -p "$evidence" "$dbroot"
if [ ! -d "$dbroot/data" ]; then
  "$pgbin/initdb" -D "$dbroot/data" -U postgres -A trust --locale=C --encoding=UTF8 > "$evidence/postgres-init.log" 2>&1
fi
"$pgbin/pg_ctl" -D "$dbroot/data" -o "-h 127.0.0.1 -p 56941 -c unix_socket_directories='' -c shared_buffers=128MB -c max_connections=100" -l "$dbroot/server.log" -w start > "$evidence/$label-postgres-start.log" 2>&1
trap '"$pgbin/pg_ctl" -D "$dbroot/data" -m fast -w stop > "$evidence/$label-postgres-stop.log" 2>&1 || true' EXIT
export PGBASE_TEST_ADMIN_URL='postgres://postgres@127.0.0.1:56941/postgres?sslmode=disable'
export GOMAXPROCS=2
export GOMEMLIMIT=512MiB
export PATH="$gobin:$pgbin:$PATH"
git rev-parse HEAD > "$evidence/$label.source-sha"
set +e
"$gobin/go" test -ldflags=-w -count=1 -p=1 -timeout=180s "$@" -json > "$evidence/$label.jsonl" 2> "$evidence/$label.stderr"
result=$?
set -e
printf '%s\n' "$result" > "$evidence/$label.exit"
python3 - "$evidence/$label.jsonl" <<'PY'
import json,sys
rows=[json.loads(l) for l in open(sys.argv[1]) if l.startswith('{')]
print(json.dumps({'source':sys.argv[1],'packages':[r for r in rows if r.get('Action') in ('pass','fail') and 'Test' not in r],'failed':[r.get('Test') for r in rows if r.get('Action')=='fail' and 'Test' in r],'skips':[r.get('Test') for r in rows if r.get('Action')=='skip'],'passed_top_level_tests':sum(r.get('Action')=='pass' and 'Test' in r and '/' not in r['Test'] for r in rows)}))
PY
exit "$result"
