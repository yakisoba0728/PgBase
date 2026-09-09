# N8 independent Standards and Spec review

## Review pin and verdict

- Review branch: `codex/n8-review`
- Backend candidate: `10d9df44dded8ed908dc277c91852d3ab2554955` (`feat: configure SQL console bounds`)
- Fixed comparison base: `91a0bdac9212cdf26207cdba619fc579b359b885`
- Component anchors supplied by the owner: API `437995a`, service `f9840d0`, executor `4b46e1c`
- Verdict: **changes required**. Four P1 defects block backend acceptance.
- Scope: N8 executor, service, API, parser/catalog checks, database roles, ledger/receipt flows, migration, and committed options. The final closure must review only the later stable Options/shared-route/navigation/UI and lead-composition delta plus backend fixes.

No `AGENTS.md` exists in this repository or its checked parent directories. The review used `docs/workstreams/n6-n9/N8.md`, N8 section 6 and acceptance gates in `docs/superpowers/plans/2026-09-10-n6-n9-execution.md`, and the code-review skill. The user explicitly prohibited subagents, so the same independent reviewer performed the Standards and Spec axes separately.

The lead-reviewed parser/clone correction represented by owner commit `93dda0c` (lead equivalent `aea47ab`) was treated as closed and was not raised again.

## Blocking findings

### N8-R1 — P1 — catalog policy is frozen when pools open

**Path:** `internal/sqlconsole/pools.go:389-400`, `internal/sqlconsole/pools.go:501-525`; supporting schema behavior in `internal/schema/planner.go:168-229` and `internal/schema/planner.go:513-546`.

`managedReadColumns` derives the readable managed columns from the startup catalog, and every later `ValidatePlan` call rebuilds policy from that same frozen `pools.catalog`. A schema update can change an existing field to `Hidden` without physical column DDL, so the already-open read role retains its column grant and the runner continues to accept the field after it becomes protected.

This violates N8.2/N8.4 fail-closed catalog-version revalidation and the prohibition on protected or secret reads. A focused PG17/18 regression should open pools with a visible field, update the field to `Hidden` through the schema service, then prove that the same runner rejects the column before execution.

### N8-R2 — P1 — an unconfirmed commit is recorded as a definite failure

**Path:** `internal/sqlconsole/runner.go:171-184`, `internal/sqlconsole/runner.go:505-526`, `internal/sqlconsole/service.go:259-275`, `internal/sqlconsole/ledger.go:127-142`, `internal/sqlconsole/ledger.go:154-183`.

After a commit transport error, receipt reconciliation returns `(false, nil)` when the new connection sees no receipt. The runner then returns the original commit error instead of `ErrCommitUnknown`. The service therefore records failed, canceled, or timed out with `committed=false`; startup reconciliation never revisits those terminal states. A receipt that becomes visible later can permanently contradict the durable execution row.

This violates N8.4's commit-unknown contract and the rule that a commit/cancel race cannot be reported as rolled back without proof. The focused test must inject a non-PostgreSQL commit error with no immediately visible receipt, assert `unknown` and nullable commit state, then expose the receipt and assert reconciliation to `succeeded`/`committed=true`.

### N8-R3 — P1 — valid virtual fields prevent SQL console startup

**Path:** `internal/sqlconsole/pools.go:389-435`; supporting physical-schema behavior in `internal/schema/planner.go:580-598`.

`managedReadColumns` expects every non-password, non-hidden catalog field to exist as a base-table column. The schema planner intentionally creates no base-table column for file fields or multiple-cardinality relation fields. `validateColumnReads` consequently rejects a valid catalog because those expected columns are absent.

This violates N8.2 startup behavior for supported PgBase catalogs. A focused PG17/18 fixture should create a valid collection with a file field and a multiple relation, grant only actual readable columns, and prove `OpenPools` succeeds while the virtual fields remain unavailable to SQL.

### N8-R4 — P1 — one service startup abandons another instance's active work

**Path:** `internal/sqlconsole/service.go:99-114`, `internal/sqlconsole/service.go:278-295`, `internal/sqlconsole/ledger.go:132-142`, `internal/sqlconsole/ledger.go:154-183`.

Every `NewServiceWithOptions` call immediately reconciles all shared `pending`, `running`, and `unknown` rows. There is no instance ownership, lease, age threshold, or exclusion lock. Starting service B while service A is executing marks A's live row `failed/abandoned/committed=false`. A can still commit its transaction and receipt; its status-guarded `Ledger.Finish` then conflicts, and `finishWithoutRun` discards that error.

This violates the durable state and commit reconciliation requirements in N8.2-log/N8.4. A focused PG17/18 test should run a blocked write in service A, construct service B against the same control database, release A, and prove that the durable row cannot contradict the committed receipt and data.

## Standards review

The candidate is clean under `git diff --check`, `gofmt -d`, and `go vet` for the changed backend packages. Changed production modules stay below the repository's 800-line ceiling. Naming and package boundaries are consistent with nearby code, and the code uses structured errors rather than panics for request failures.

The principal maintainability concern is that policy snapshots and durable state transitions are distributed across `Pools`, `Runner`, `Service`, and `Ledger` without one owner for freshness or transition invariants. N8-R1 and N8-R4 are concrete consequences, so they are recorded as product defects rather than style observations.

One additional bounded candidate remains for fix-delta review: `inspectWriteTargets` stores every physical column in `targetSnapshot.Columns` at `internal/sqlconsole/pools.go:221-255`, while `ValidatePlan` treats that map as the configured write-column allowlist at `internal/sqlconsole/pools.go:515-523`. Exact database grants currently provide a second rejection layer, but the requested pre-execution configured-column guard appears to be lost. This needs a focused fixture before severity is finalized.

## Spec review

The reviewed implementation otherwise has the expected core shape: read transactions are read-only and rolled back; writes use one transaction and one commit after caps; dry-run, statement errors, and cancellation roll back; cancellation is sent only for the borrowed connection and that connection is discarded; parameters use extended-protocol bindings; result cells preserve explicit null/numeric/JSON/bytes/temporal tags; raw SQL, parameters, and results are absent from durable rows; API ownership is actor/session scoped; and startup role checks reject broad role, schema, relation, and receipt privileges.

The following required evidence is missing and is not counted as an additional product defect yet:

- PostgreSQL-native dependency/OID inspection for implicit casts, operators, aggregate/window functions, and transitive view dependencies. The analyzer records names, while `Pools.ValidatePlan` currently validates only relations and write columns. The source plan requires an exact builtin OID allowlist and fail-closed transitive inspection.
- Full PG17 and PG18 startup-role, side-effect target, transaction, cap, cancel/discard, receipt, and restart matrix. The repository tests require `PGBASE_TEST_ADMIN_URL`; no isolated URLs have been authorized yet.
- Browser evidence for the independent SQL page, cancellation recovery, exact-value copy, write targets, multi-statement outcomes, and unknown final state. UI was intentionally outside this backend pass.
- A fixed durable history/receipt retention policy and a no-mutation-replay proof after retention. `N8.md` says the policy must be set before migration, but the current migration only supplies cleanup indexes.

Two source-contract questions remain for lead composition: the full execution plan says read mode is a single `SELECT`/`VALUES`, while the implementation accepts up to ten read statements; and the result model exposes `Truncated`, but the runner currently hard-fails the row/byte cap rather than producing a truncated result. These are recorded as contract/evidence gaps until the lead resolves the intended behavior.

## Verification evidence

Passed against `10d9df4` without a database:

- `go test ./internal/sqlconsole -run 'TestAnalyze|TestNormalize|TestRequest|TestParameter|TestCell|TestClone|TestCanonical' -count=1`
- `go test -race ./internal/sqlconsole -run 'TestAnalyze|TestNormalize|TestRequest|TestParameter|TestCell|TestClone|TestCanonical' -count=1`
- `go test ./internal/api -run '^TestSQLConsole' -count=1`
- `go test ./pgbase -run '^TestSQLConsoleOptions' -count=1`
- `go vet ./internal/sqlconsole ./internal/api ./pgbase ./migrations/system`
- `git diff --check 91a0bdac...10d9df4`
- `gofmt -d` on the N8 Go delta

The broad package test compiled and passed `internal/api` and `pgbase`; database integration tests in `internal/sqlconsole` and `migrations/system` stopped because `PGBASE_TEST_ADMIN_URL` is unset. That is an environment evidence gap, not a test failure attributed to the candidate.

No database was started or mutated. Ports `8090`, `55440`, and `59000` and all user data remain untouched. Euclid reserved project `pgbase-n8-20260910` with PG17/18 ports `55470`/`55471`; this review will wait for explicit fixture URLs and coordination before mutating checks. API port `58096` is also reserved for the later browser/API pass.

## Remaining handoff

Euclid and Linnaeus received N8-R1 through N8-R4 with path, line, contract, and focused regression requirements. Backend closure requires stable owner fix SHAs plus the authorized PG17/18 evidence. Final same-reviewer closure then reviews only those backend fix deltas and the stable Options/shared route/navigation/UI/lead-composition delta; unchanged backend code will not be re-reviewed.
