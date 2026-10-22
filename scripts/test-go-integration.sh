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
if [[ -n "${PGBASE_INTEGRATION_EXCLUDE_PACKAGES:-}" ]]; then
	read -r -a excluded <<<"$PGBASE_INTEGRATION_EXCLUDE_PACKAGES"
	for package in "${excluded[@]}"; do
		if [[ ! "$package" =~ ^\./[A-Za-z0-9_./-]+$ ]]; then
			echo "PGBASE_INTEGRATION_EXCLUDE_PACKAGES contains an invalid package path: $package" >&2
			exit 2
		fi
	done
	# A shard runs every selected package except those another shard owns.
	excluded_paths="$(go list -tags=integration "${excluded[@]}")"
	all_paths="$(go list -tags=integration "${packages[@]}")"
	selected=()
	while IFS= read -r package; do
		[[ -n "$package" ]] && selected+=("$package")
	done <<<"$(grep -vxF "$excluded_paths" <<<"$all_paths" || true)"
	if ((${#selected[@]} == 0)); then
		echo "PGBASE_INTEGRATION_EXCLUDE_PACKAGES leaves no package to test" >&2
		exit 2
	fi
	packages=("${selected[@]}")
fi

set +e
go test -json -tags=integration "$@" "${packages[@]}" | go run ./tools/testskip -allowlist tests/go-test-skip-allowlist.tsv
pipeline_status=("${PIPESTATUS[@]}")
set -e

if (( pipeline_status[0] != 0 )); then
	exit "${pipeline_status[0]}"
fi
exit "${pipeline_status[1]}"
