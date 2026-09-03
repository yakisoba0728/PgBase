#!/usr/bin/env bash

# promtool has no expression-only check, so every dashboard query is wrapped as
# a recording rule and handed to the same parser that validates the alert rules.
# A dashboard panel that cannot be parsed is a broken panel, not a cosmetic one.

set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
dashboard="$repository_root/deployments/observability/grafana-dashboard.json"
prometheus_image="prom/prometheus@sha256:5ce7540c3c00ef4ab0c9d2c995c6a5b9c421f44b4a115d97a2c7af3b1c21cbb0"
work_directory="$(mktemp -d)"
trap 'rm -rf "$work_directory"' EXIT

# The pinned Prometheus image runs promtool as a non-root user. mktemp creates
# the host directory with mode 0700, so a Linux bind mount otherwise leaves the
# container unable to traverse /rules (Docker Desktop does not expose this in
# the same way). The generated file contains only public dashboard expressions.
chmod 0755 "$work_directory"

# Grafana template variables are dashboard syntax, not PromQL. Substituting the
# selectors the dashboard declares keeps the parser checking the query shape
# instead of rejecting the templating.
expressions="$(jq --exit-status --raw-output '
  [.. | objects | select(has("expr")) | .expr]
  | map(gsub("\\$job"; "pgbase") | gsub("\\$instance"; "pgbase:8090"))
  | .[]' "$dashboard")"

if [[ -z "$expressions" ]]; then
  echo "no dashboard expressions were extracted from $dashboard" >&2
  exit 1
fi

{
  echo "groups:"
  echo "  - name: pgbase-dashboard-expressions"
  echo "    rules:"
  index=0
  while IFS= read -r expression; do
    index=$((index + 1))
    printf '      - record: pgbase:dashboard:panel%d\n' "$index"
    printf '        expr: |\n'
    printf '          %s\n' "$expression"
  done <<< "$expressions"
} > "$work_directory/dashboard-expressions.yml"
chmod 0644 "$work_directory/dashboard-expressions.yml"

docker run --rm --entrypoint=/bin/promtool \
  --volume "$work_directory:/rules:ro" \
  "$prometheus_image" \
  check rules /rules/dashboard-expressions.yml
