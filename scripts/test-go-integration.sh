#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${PGBASE_TEST_ADMIN_URL:-}" ]]; then
	echo "PGBASE_TEST_ADMIN_URL is required before the PostgreSQL integration suite starts" >&2
	exit 2
fi

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

set +e
go test -json -tags=integration "$@" ./... | go run ./tools/testskip -allowlist tests/go-test-skip-allowlist.tsv
pipeline_status=("${PIPESTATUS[@]}")
set -e

if (( pipeline_status[0] != 0 )); then
	exit "${pipeline_status[0]}"
fi
exit "${pipeline_status[1]}"
