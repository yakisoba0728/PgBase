#!/usr/bin/env bash
# Re-measures the administrator UI structural budgets with tools/repostats and
# writes the measured values into internal/repositorypolicy/loc_budget_test.go.
# repostats reads `git ls-files`, so commit (or at least `git add`) new UI files
# first. Go-side limits are deliberately left alone: only the UI rewrite moves.
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repository_root"

metrics="$(go run ./tools/repostats)"
value() { jq -r ".metrics.$1" <<<"$metrics"; }

export BUDGET_RUNTIME="$(value runtime.handwritten_total.loc)"
export BUDGET_UI="$(value runtime.ui.loc)"
export BUDGET_PAGE_HOOKS="$(value ui.page_direct_use_state_effect_occurrences)"
export BUDGET_UI_EXPORTS="$(value ui.runtime_exports)"

perl -pi -e '
  my %limits = (
    "handwritten runtime lines" => $ENV{BUDGET_RUNTIME},
    "UI runtime lines" => $ENV{BUDGET_UI},
    "UI page direct useState/useEffect" => $ENV{BUDGET_PAGE_HOOKS},
    "UI runtime exports" => $ENV{BUDGET_UI_EXPORTS},
  );
  s/^(\t\{"([^"]+)", func\(m repositoryMetrics\) int \{ return [^}]+\}, )\d+/exists $limits{$2} ? $1 . $limits{$2} : $&/e;
' internal/repositorypolicy/loc_budget_test.go
test "$(grep -cE '^	\{"(handwritten runtime lines|UI runtime lines|UI page direct useState/useEffect|UI runtime exports)", ' internal/repositorypolicy/loc_budget_test.go)" = 4

echo "runtime=$BUDGET_RUNTIME ui=$BUDGET_UI page_hooks=$BUDGET_PAGE_HOOKS ui_exports=$BUDGET_UI_EXPORTS"
