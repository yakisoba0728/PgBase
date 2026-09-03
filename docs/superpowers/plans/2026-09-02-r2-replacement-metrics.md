# R2 replacement metrics

This report fixes the R2 measurement boundary before R2b changes the migration
and schema surfaces again.

- Base: `f0c59cc5a14b7c254b76774664ee6b7d40797cf8`
- Target: `78ec383`
- Command: `go run ./tools/repostats --compare f0c59cc --pretty`

## Replacement delta

| Metric | Base | Target | Delta |
| --- | ---: | ---: | ---: |
| Production Go LOC | 20,499 | 21,664 | +1,165 |
| Production Go interfaces | 19 | 17 | -2 |
| Exported Go type/function/method declarations | 342 | 368 | +26 |
| Go constructors beginning with `New` | 24 | 27 | +3 |

The full R2 range, including the deterministic test-suite split and its
support tooling, contains 8,443 additions and 4,583 deletions (net +3,860).
Production Go accounts for 1,530 additions and 365 deletions (net +1,165).
The exported increase is the typed physical-schema manifest plus the sealed
schema change/apply constructors and catalog rollout errors. The removed
interfaces include the planner abstraction; no public scalar registry was
added because its deletion/LOC spike failed the adoption test.

`internal/schema/service.go` fell below the 800-LOC long-file threshold. The
remaining long production files at this boundary are
`internal/recordops/service.go` (1,221 LOC) and
`internal/schema/planner.go` (947 LOC).

## Gate result

The target passed all of the following from a clean worktree:

- `./scripts/test-go-unit.sh -race -count=1`
- `./scripts/lint-go.sh`
- `go vet ./...`
- `./scripts/test-go-integration.sh -race -count=1` against PostgreSQL
  16.13, 17.9, and 18.3

The PostgreSQL matrix includes exact physical-manifest catalog checks,
schema/apply versus record-pin concurrency, legacy catalog rollout preflight,
canonical 16 MiB wire boundaries, and listener/cache publication races.
