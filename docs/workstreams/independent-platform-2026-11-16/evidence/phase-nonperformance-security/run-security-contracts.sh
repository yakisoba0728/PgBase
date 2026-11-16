#!/usr/bin/env bash
set -euo pipefail
qa_repo=/workspace/scratch/a53362cf26a0/PgBase-qa
qa_pg=/workspace/scratch/2a97293516a0/toolchains/postgres/bin
qa_go=/workspace/scratch/2a97293516a0/toolchains/go/bin
qa_data=/workspace/scratch/a53362cf26a0/qa-pg/data-utf8
qa_evidence="$qa_repo/docs/workstreams/independent-platform-2026-11-16/evidence/phase-nonperformance-security"
qa_label=$1
shift
cd "$qa_repo"
"$qa_pg/pg_ctl" -D "$qa_data" -o "-h 127.0.0.1 -p 56873 -c unix_socket_directories='' -c shared_buffers=128MB -c max_connections=120" -l /workspace/scratch/a53362cf26a0/qa-pg/server.log -w start > "$qa_evidence/$qa_label-pg-start.log" 2>&1
trap '"$qa_pg/pg_ctl" -D "$qa_data" -m fast -w stop > "$qa_evidence/$qa_label-pg-stop.log" 2>&1 || true' EXIT
export PGBASE_TEST_ADMIN_URL='postgres://postgres@127.0.0.1:56873/postgres?sslmode=disable'
export GOMAXPROCS=2
export PATH="$qa_go:$qa_pg:$PATH"
set +e
"$qa_go/go" test -count=1 -p=1 -timeout=180s "$@" -json > "$qa_evidence/$qa_label.jsonl" 2> "$qa_evidence/$qa_label.stderr"
qa_result=$?
set -e
printf '%s\n' "$qa_result" > "$qa_evidence/$qa_label.exit"
python3 - "$qa_evidence/$qa_label.jsonl" <<'PY'
import json,sys
rows=[json.loads(line) for line in open(sys.argv[1]) if line.startswith('{')]
print(json.dumps({'log':sys.argv[1], 'packages':[r for r in rows if r.get('Action') in ('pass','fail') and 'Test' not in r], 'failed':[r.get('Test') for r in rows if r.get('Action')=='fail' and 'Test' in r], 'skips':[r.get('Test') for r in rows if r.get('Action')=='skip'], 'passed_top_level_tests':sum(r.get('Action')=='pass' and 'Test' in r and '/' not in r['Test'] for r in rows)}))
for r in rows:
    if 'schema68 restore reset' in r.get('Output',''):
        print(r['Output'].strip())
PY
exit "$qa_result"
