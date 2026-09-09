#!/usr/bin/env bash

set -euo pipefail
umask 022

if (($# != 2)); then
	echo "usage: build-release-phase-artifacts.sh ROLLOUT OUTPUT_DIRECTORY" >&2
	exit 2
fi

rollout="$1"
output_directory="$2"
if [[ ! "$rollout" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
	echo "release rollout name is invalid" >&2
	exit 2
fi

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
manifest="$repository_root/deployments/release-phases.json"
if ! git -C "$repository_root" rev-parse --git-dir >/dev/null 2>&1; then
	echo "release phase artifacts require the repository's Git objects" >&2
	exit 1
fi
if [[ ! -f "$manifest" ]]; then
	echo "release phase manifest is missing" >&2
	exit 1
fi
if ! jq --exit-status '.schemaVersion == 1' "$manifest" >/dev/null; then
	echo "unsupported release phase manifest schema" >&2
	exit 1
fi
if [[ "$(jq --arg rollout "$rollout" '[.rollouts[] | select(.name == $rollout)] | length' "$manifest")" != 1 ]]; then
	echo "release rollout must appear exactly once in the manifest: $rollout" >&2
	exit 1
fi

mkdir -p -- "$output_directory"
output_directory="$(cd -- "$output_directory" && pwd)"
build_workspace="$(mktemp -d)"
metadata_lines="$build_workspace/artifacts.jsonl"
toolchain="go1.26.8"
target_goos="${PGBASE_RELEASE_ARTIFACT_GOOS:-$(GOTOOLCHAIN="$toolchain" go env GOOS)}"
target_goarch="${PGBASE_RELEASE_ARTIFACT_GOARCH:-$(GOTOOLCHAIN="$toolchain" go env GOARCH)}"
go_version="$(GOTOOLCHAIN="$toolchain" go env GOVERSION)"
if [[ ! "$target_goos" =~ ^[a-z0-9]+$ || ! "$target_goarch" =~ ^[a-z0-9]+$ ]]; then
	echo "release artifact target is invalid: $target_goos/$target_goarch" >&2
	exit 2
fi

cleanup() {
	rm -rf -- "$build_workspace"
}
trap cleanup EXIT

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

jq -r --arg rollout "$rollout" \
	'.rollouts[] | select(.name == $rollout) |
		((if .predecessor then [{phase:"Predecessor", commit:.predecessor.commit, version:.predecessor.version}] else [] end) +
		[.artifacts[] | {phase, commit, version}] +
		(if .operator then [{phase:"Operator", commit:.operator.commit, version:.operator.version}] else [] end))[] |
		[.phase, .commit, .version] | @tsv' \
	"$manifest" |
while IFS=$'\t' read -r phase commit version; do
	if [[ ! "$phase" =~ ^[A-Za-z][A-Za-z0-9]*$ || ! "$commit" =~ ^[0-9a-f]{40}$ || -z "$version" ]]; then
		echo "release artifact phase, commit, or version is invalid: $phase $commit $version" >&2
		exit 1
	fi
	if ! git -C "$repository_root" cat-file -e "$commit^{commit}" 2>/dev/null; then
		echo "release artifact source commit is unavailable: $phase $commit" >&2
		exit 1
	fi
	if ! git -C "$repository_root" merge-base --is-ancestor "$commit" HEAD; then
		echo "release artifact source is not an ancestor of HEAD: $phase $commit" >&2
		exit 1
	fi
	"$repository_root/scripts/support/release-tag-policy.sh" "$version" "$version" >/dev/null

	phase_name="$(printf '%s' "$phase" | tr '[:upper:]' '[:lower:]')"
	source_directory="$build_workspace/source-$phase_name"
	mkdir "$source_directory"
	git -C "$repository_root" archive "$commit" | tar -x -C "$source_directory"
	built_at="$(git -C "$repository_root" show -s --format=%cI "$commit")"
	schema_version="$(find "$source_directory/migrations/system" -maxdepth 1 -type f -name '[0-9][0-9][0-9][0-9][0-9][0-9]_*.sql' -print |
		sed -E 's|.*/0*([0-9]+)_.*|\1|' | sort -n | tail -1)"
	if [[ -z "$built_at" || "$built_at" == *[[:space:]]* || ! "$schema_version" =~ ^[1-9][0-9]*$ ]]; then
		echo "release artifact build metadata is invalid: $phase" >&2
		exit 1
	fi
	ldflags="-s -w -X github.com/webisnoteasy/PgBase/pgbase.Version=$version"
	ldflags+=" -X github.com/webisnoteasy/PgBase/internal/buildinfo.Commit=$commit"
	ldflags+=" -X github.com/webisnoteasy/PgBase/internal/buildinfo.BuiltAt=$built_at"
	ldflags+=" -X github.com/webisnoteasy/PgBase/internal/buildinfo.SchemaMin=$schema_version"
	ldflags+=" -X github.com/webisnoteasy/PgBase/internal/buildinfo.SchemaMax=$schema_version"
	artifact="$output_directory/pgbase-$rollout-$phase_name"
	(
		cd "$source_directory"
		GOTOOLCHAIN="$toolchain" GOOS="$target_goos" GOARCH="$target_goarch" CGO_ENABLED=0 \
			go build -buildvcs=false -trimpath \
			-ldflags "$ldflags" \
			-o "$artifact" ./cmd/pgbase
	)
	if [[ "$target_goos" == "$(GOTOOLCHAIN="$toolchain" go env GOOS)" &&
	      "$target_goarch" == "$(GOTOOLCHAIN="$toolchain" go env GOARCH)" &&
	      -f "$source_directory/internal/buildinfo/buildinfo.go" ]]; then
		expected="{\"version\":\"$version\",\"commit\":\"$commit\",\"builtAt\":\"$built_at\",\"goos\":\"$target_goos\",\"goarch\":\"$target_goarch\",\"schemaMin\":\"$schema_version\",\"schemaMax\":\"$schema_version\"}"
		if [[ "$("$artifact" version --json)" != "$expected" ]]; then
			echo "release artifact build metadata does not match its inputs: $phase" >&2
			exit 1
		fi
	fi
	chmod 0755 "$artifact"
	digest="$(sha256_file "$artifact")"
	jq -nc --arg phase "$phase" --arg commit "$commit" --arg path "$(basename "$artifact")" \
		--arg version "$version" --arg builtAt "$built_at" --arg schemaVersion "$schema_version" \
		--arg digest "sha256:$digest" --arg goVersion "$go_version" \
		--arg goOS "$target_goos" --arg goArch "$target_goarch" \
		'{phase:$phase,commit:$commit,version:$version,path:$path,digest:$digest,
			build:{builtAt:$builtAt,schemaMin:$schemaVersion,schemaMax:$schemaVersion},
			toolchain:{goVersion:$goVersion,goOS:$goOS,goArch:$goArch}}' >>"$metadata_lines"
done

jq -s --arg rollout "$rollout" \
	'{schemaVersion:1,rollout:$rollout,artifacts:.}' "$metadata_lines" \
	>"$output_directory/$rollout-artifacts.json"
