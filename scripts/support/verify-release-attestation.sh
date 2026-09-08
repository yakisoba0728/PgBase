#!/usr/bin/env bash

set -euo pipefail

if (($# != 5)); then
  echo "usage: verify-release-attestation.sh CANDIDATE_IMAGE PREDICATE_TYPE EXPECTED_PREDICATE PUBLIC_KEY EVIDENCE" >&2
  exit 2
fi

candidate_image="$1"
predicate_type="$2"
expected_predicate="$3"
public_key="$4"
evidence="$5"
script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cosign_binary="${PGBASE_COSIGN:-cosign}"

"$script_directory/validate-release-candidate.sh" "$candidate_image" "${EXPECTED_VERSION:?EXPECTED_VERSION is required}"
test -s "$expected_predicate"
test -s "$public_key"

cosign_arguments=(
  verify-attestation
  --key "$public_key"
  --insecure-ignore-tlog
  --type "$predicate_type"
)
if [[ "${PGBASE_COSIGN_ALLOW_INSECURE_REGISTRY:-}" == "1" ]]; then
  cosign_arguments+=(--allow-insecure-registry)
fi

raw_evidence="${evidence}.raw"
"$cosign_binary" "${cosign_arguments[@]}" "$candidate_image" > "$raw_evidence"
expected_digest="${candidate_image##*@sha256:}"
expected_name="${candidate_image%@sha256:*}"

# Cosign verifies the signature and image claim. This second check makes the
# release policy explicit: at least one valid statement must contain the exact
# predicate produced by this run and the exact digest-pinned subject.
jq --exit-status --slurp \
  --arg digest "$expected_digest" \
  --arg name "$expected_name" \
  --slurpfile expected "$expected_predicate" '
    [ .[]
      | (if type == "array" then .[] else . end)
      | (.payload | @base64d | fromjson)
      | select(.subject == [{name: $name, digest: {sha256: $digest}}])
      | select(.predicate == $expected[0])
    ]
    | if length > 0 then . else error("no attestation matches the exact subject and predicate") end
  ' "$raw_evidence" > "$evidence"
