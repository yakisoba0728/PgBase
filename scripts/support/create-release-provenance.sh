#!/usr/bin/env bash

set -euo pipefail

if (($# != 3)); then
  echo "usage: create-release-provenance.sh OUTPUT EXPECTED_VERSION SOURCE_COMMIT" >&2
  exit 2
fi

output="$1"
expected_version="$2"
source_commit="$3"

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_REF:?GITHUB_REF is required}"
: "${GITHUB_WORKFLOW_REF:?GITHUB_WORKFLOW_REF is required}"
: "${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"
: "${GITHUB_RUN_ATTEMPT:?GITHUB_RUN_ATTEMPT is required}"
: "${GITHUB_SERVER_URL:?GITHUB_SERVER_URL is required}"

if [[ ! "$source_commit" =~ ^[0-9a-f]{40}$ ]] || [[ ! "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]; then
  echo "source and workflow commits must be full lowercase object IDs" >&2
  exit 2
fi

repository_url="$GITHUB_SERVER_URL/$GITHUB_REPOSITORY"
invocation_url="$repository_url/actions/runs/$GITHUB_RUN_ID/attempts/$GITHUB_RUN_ATTEMPT"

# This is an assertion made by PgBase's persistent release key. It records the
# GitHub-hosted build context without claiming a GitHub OIDC/Fulcio identity.
jq --null-input \
  --arg buildType "https://github.com/yakisoba0728/PgBase/attestations/container-build/v1" \
  --arg repository "$repository_url" \
  --arg ref "$GITHUB_REF" \
  --arg version "$expected_version" \
  --arg sourceCommit "$source_commit" \
  --arg workflowRef "$GITHUB_WORKFLOW_REF" \
  --arg workflowCommit "$GITHUB_SHA" \
  --arg invocationId "$invocation_url" \
  '{buildDefinition: {
      buildType: $buildType,
      externalParameters: {repository: $repository, ref: $ref, version: $version},
      internalParameters: {workflow: {ref: $workflowRef, commit: $workflowCommit}},
      resolvedDependencies: [{uri: ("git+" + $repository + "@" + $sourceCommit), digest: {gitCommit: $sourceCommit}}]
    },
    runDetails: {
      builder: {id: "https://github.com/actions/runner/github-hosted"},
      metadata: {invocationId: $invocationId}
    }}' > "$output"
