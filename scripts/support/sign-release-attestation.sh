#!/usr/bin/env bash

set -euo pipefail

if (($# != 3)); then
  echo "usage: sign-release-attestation.sh CANDIDATE_IMAGE PREDICATE_TYPE PREDICATE_PATH" >&2
  exit 2
fi

candidate_image="$1"
predicate_type="$2"
predicate_path="$3"
script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cosign_binary="${PGBASE_COSIGN:-cosign}"

: "${COSIGN_PRIVATE_KEY:?COSIGN_PRIVATE_KEY is required}"
: "${COSIGN_PASSWORD:?COSIGN_PASSWORD is required}"
"$script_directory/validate-release-candidate.sh" "$candidate_image" "${EXPECTED_VERSION:?EXPECTED_VERSION is required}"
test -s "$predicate_path"
jq --exit-status 'type == "object"' "$predicate_path" >/dev/null

# The empty signing config disables Fulcio, Rekor and timestamp services. The
# encrypted key comes directly from the environment and is never written out.
cosign_arguments=(
  attest
  --yes
  --key env://COSIGN_PRIVATE_KEY
  --signing-config "$script_directory/cosign-private-signing-config.json"
  --type "$predicate_type"
  --predicate "$predicate_path"
)
if [[ "${PGBASE_COSIGN_ALLOW_INSECURE_REGISTRY:-}" == "1" ]]; then
  cosign_arguments+=(--allow-insecure-registry)
fi

"$cosign_binary" "${cosign_arguments[@]}" "$candidate_image"
