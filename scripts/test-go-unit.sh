#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

# Unit tests must remain independent of any developer database, even when the
# caller has a live test URL exported in their shell.
unset PGBASE_TEST_ADMIN_URL

set +e
go test -json "$@" ./... | go run ./tools/testskip -allowlist tests/go-test-skip-allowlist.tsv
pipeline_status=("${PIPESTATUS[@]}")
set -e

if (( pipeline_status[0] != 0 )); then
	exit "${pipeline_status[0]}"
fi
exit "${pipeline_status[1]}"
