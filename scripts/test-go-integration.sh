#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${PGBASE_TEST_ADMIN_URL:-}" ]]; then
	echo "PGBASE_TEST_ADMIN_URL is required before the PostgreSQL integration suite starts" >&2
	exit 2
fi

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

packages=(./...)
if [[ -n "${PGBASE_INTEGRATION_PACKAGES:-}" ]]; then
	read -r -a packages <<<"$PGBASE_INTEGRATION_PACKAGES"
	for package in "${packages[@]}"; do
		if [[ ! "$package" =~ ^\./[A-Za-z0-9_./-]+$ ]]; then
			echo "PGBASE_INTEGRATION_PACKAGES contains an invalid package path: $package" >&2
			exit 2
		fi
	done
fi

set +e
go test -json -tags=integration "$@" "${packages[@]}" | go run ./tools/testskip -allowlist tests/go-test-skip-allowlist.tsv
pipeline_status=("${PIPESTATUS[@]}")
set -e

if (( pipeline_status[0] != 0 )); then
	exit "${pipeline_status[0]}"
fi
exit "${pipeline_status[1]}"
