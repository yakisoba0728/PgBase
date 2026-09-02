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

docker run --rm --entrypoint=/bin/promtool \
  --volume "$work_directory:/rules:ro" \
  "$prometheus_image" \
  check rules /rules/dashboard-expressions.yml
