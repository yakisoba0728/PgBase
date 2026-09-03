# R2b schema-operation ledger metrics

This report fixes the R2b measurement boundary after the durable receipt,
reconciliation, feature-release compatibility, and lifecycle stack.

- Base: `9278523e9da7fd7cce3579eb73c2921433f49c0f`
- Target: `bc5c16cb982da956c6d410dc40b51dd6078811ec`
- Command: `go run ./tools/repostats --compare 9278523 --pretty`

## Stack delta

| Metric | Base | Target | Delta |
| --- | ---: | ---: | ---: |
| Production Go LOC | 22,221 | 24,138 | +1,917 |
| Production Go interfaces | 17 | 17 | 0 |
| Exported Go type/function/method declarations | 375 | 394 | +19 |
| Go constructors beginning with `New` | 27 | 29 | +2 |
| Runtime comment-only lines | 932 | 957 | +25 |

The full R2b range contains 4,370 additions and 226 deletions (net +4,144).
Production runtime accounts for 2,113 additions and 196 deletions (net
+1,917); tests account for 2,241 additions and 23 deletions; the rollout
runbook accounts for 16 additions and 7 deletions. This is an additive
correctness/migration stack: the new runtime is the bounded claim, fenced DDL,
receipt history, reconciliation, and cleanup path required by the approved
R2b contract.

The old monolithic schema-apply transaction and its separate
`commitSchemaChange` helper were deleted. Legacy create/update/delete callers
now enter the same durable operation path, so no parallel raw schema-apply
implementation remains. The feature artifact also removed the preparatory P0
compatibility default and requires clean v6 history. The two private scan/query
interfaces found during the quantitative review were removed in favor of
pgx's existing row contract, leaving the production interface count flat.
Owner-history pagination was separated from claim/transition storage, so R2b
adds no production file above 800 LOC; the pre-existing long files remain
`internal/recordops/service.go` and `internal/schema/planner.go`.

## Gate result

The target stack passed:

- `go test ./...` and `go test -race ./...`;
- `go vet ./...`, `go build ./...`, and `scripts/lint-go.sh`;
- `scripts/test-go-integration.sh -count=1` with skip enforcement against
  PostgreSQL 16, 17, and 18;
- the full PostgreSQL 16 integration race suite;
- the immutable v1-v6 migration checksum sentinel;
- an independent adversarial audit after the rollout, lease, reconciliation,
  historical-operation-ID, cursor-skew, and test-placement fixes.
