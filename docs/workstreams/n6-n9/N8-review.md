# N8 independent Standards and Spec review

## Paused checkpoint — 2026-09-10

The user stopped this review at its current boundary. This document is an incomplete review checkpoint, **not a full-pass or acceptance verdict**. No further review, repro, test, database, or product work was performed after the stop request.

Exact reviewed commits:

- Backend candidate `10d9df44dded8ed908dc277c91852d3ab2554955`, compared with `91a0bdac9212cdf26207cdba619fc579b359b885`.
- Supplied backend component anchors: API `437995a`, service `f9840d0`, executor `4b46e1c`.
- Shared route/UI `5e2ed00b64b0282c49ee60e653ecd70321abfa7f` and read-mode follow-up `485d9af2b0b8b5878baec62a76bfef999a16b200`.
- UI recovery `16ce11ff857aa81ef7c19cd8a351b8e8bb27b7f7` (integration `c90412cfac93735f4deaa21ee281d831af78d870`).
- UI exact replay `8df87575464622b33e356c087dcc746894e86827`.
- UI session scope/durable summary `cd092fb41859812dc333774d4522586f22642757` (integration `35712f45be41f8239383824c55401727d510e0ed`).

Closed within the reviewed commits:

- The early parser/clone correction at owner `93dda0c` (lead equivalent `aea47ab`) remained closed and was not recreated.
- N8-R5 is closed by exact same-identity/body replay and reload persistence in `8df8757`; its cross-session regression became N8-R11.
- N8-R6 and N8-R7 are closed by `16ce11f`, with R6's real durable-summary shape completed by `cd092fb`.
- N8-R11 is closed in the reviewed UI delta `cd092fb`, subject to the unverified final backend session-identity capability composition below.
- The browser enforces single-statement read mode at `485d9af`; direct API enforcement remains open as N8-R10.

Open product findings at the stop point:

- P1: N8-R1 catalog freshness, N8-R2 commit-unknown reconciliation, N8-R3 virtual-field startup, N8-R4 multi-service durable coordination, and N8-R8 exact/transitive dependency OID guarding.
- P2: N8-R9 configured write-column preflight, N8-R10 direct API single-read enforcement, and N8-R12 bounded heavy-row retention with indefinite minimal no-replay tombstones.

Unverified or missing evidence, kept separate from product defects:

- No stable owner fix commit after `10d9df4` was reviewed. Private/uncommitted owner changes were not inspected.
- The backend `sessionIdentity` capability and its final Options/shared-route/UI composition were not available as a stable reviewed delta.
- The final lead integration composition after `35712f4` was not reviewed.
- The authorized isolated PG17/PG18 matrix on ports `55470`/`55471` and API/browser evidence on `58096` were not run by this reviewer. No database was started or mutated.
- Result-cap behavior (`Truncated` versus hard failure) and the remaining browser acceptance matrix remain contract/evidence gaps pending lead resolution.

Resume point: obtain a clean stable owner SHA descended from `10d9df4`, review only that backend fix delta against N8-R1–R4/R8–R10/R12 and the session-identity capability, run the authorized focused PG17/18 evidence after owner coordination, then review only the final lead-composition delta after `35712f4`. Do not re-review unchanged backend or the closed UI delta.

Reviewer-owned process state at pause: no reviewer-started database, API, test, or shell process remained running.

## Review pin and verdict

- Review branch: `codex/n8-review`
- Backend candidate: `10d9df44dded8ed908dc277c91852d3ab2554955` (`feat: configure SQL console bounds`)
- Fixed comparison base: `91a0bdac9212cdf26207cdba619fc579b359b885`
- Component anchors supplied by the owner: API `437995a`, service `f9840d0`, executor `4b46e1c`
- Shared route/UI candidate reviewed: `5e2ed00b64b0282c49ee60e653ecd70321abfa7f`
- Follow-up read-mode fix reviewed: `485d9af2b0b8b5878baec62a76bfef999a16b200`
- UI recovery fix reviewed: worker `16ce11ff857aa81ef7c19cd8a351b8e8bb27b7f7`, integrated as `c90412cfac93735f4deaa21ee281d831af78d870`
- UI exact-replay follow-up reviewed: `8df87575464622b33e356c087dcc746894e86827`
- UI session-scope and durable-summary fix reviewed: worker `cd092fb41859812dc333774d4522586f22642757`, integrated as `35712f4`
- Verdict at pause: **incomplete; changes required on the reviewed backend candidate**. This is not a full-pass verdict. Five backend P1 defects remain open; the reviewed UI findings are closed at `35712f4` subject to final backend capability composition.
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

### N8-R5 — P1 — the UI loses write identity after a lost submit response

**Path:** `ui/admin/src/pages/sql-console-page.tsx:114-126` at shared candidate `5e2ed00`.

Each submit click creates a new operation ID and write idempotency key inside the request attempt. If the server accepts or commits a write but the response is lost, the catch path retains neither identity nor request. The next click creates two new UUIDs and can execute the same mutation again; the page also cannot query the first execution by its known operation ID.

This violates N8.4's same-identity reconciliation/replay contract and the page's execute-once promise. A focused UI regression should make `submit` capture the request and reject as a transport-loss simulation, then require the page to retain and query/replay that exact identity and body before it permits a fresh write.

Worker commit `16ce11f` retains the full request and queries the original operation ID, but it does not safely resolve the case where the POST never reached the server or returned a pre-execution error. GET then returns not-found forever; reload discards the identity and permits a fresh-ID retry. Closure requires exact same-identity/body replay after absence is established, or an equally durable resolution that cannot create a fresh identity while the first outcome is uncertain.

Worker commit `8df8757` closes N8-R5 itself by persisting the attempt through reload, replaying the exact retained request on definite absence, and clearing known pre-execution rejections. Its global persistence introduces the separate N8-R11 cross-session defect below.

### N8-R6 — P2 — terminal history cannot reopen an unexpired result

**Path:** `ui/admin/src/pages/sql-console-page.tsx:53-55`, `ui/admin/src/pages/sql-console-page.tsx:149-151` at shared candidate `5e2ed00`.

Durable history entries contain summaries without result rows. Clicking `Inspect` only assigns that summary to local state; terminal status prevents polling or a GET. If `resultUntil` is present, the page says the result expired even when the timestamp is still in the future, and it never asks the service for the same-session in-memory result. The unknown-state instruction to recheck is likewise non-actionable.

A focused test should return a succeeded history summary with a future `resultUntil`, make `execution(id)` return its bounded result, click `Inspect`, and require the GET and rendered values.

### N8-R7 — P2 — the UI overrides configured timeout bounds

**Path:** `ui/admin/src/pages/sql-console-page.tsx:87`, `ui/admin/src/pages/sql-console-page.tsx:118-124` at shared candidate `5e2ed00`.

The page hardcodes `5000` and sends it on every request. This overrides `SQLConsoleOptions.DefaultTimeout`; a valid deployment whose configured maximum is below five seconds rejects the untouched form's first query, while the UI still claims every value through 30000 is valid. The API capability response exposes neither configured default nor maximum.

A focused composition test should configure a two-second default/maximum and prove the untouched page sends a valid request using the server default, or omits the override. Edited values must be validated against the configured maximum.

`16ce11f` closes N8-R6 by fetching inspected entries and checking expiry against the current time. It closes N8-R7 by omitting an untouched timeout and accepting optional server timeout capabilities. Both fixes passed their focused tests; only later composition regressions can reopen them.

### N8-R8 — P1 — managed views bypass the exact OID and transitive dependency gate

**Path:** `internal/sqlconsole/analyzer.go:72-97`, `internal/sqlconsole/analyzer.go:407-425`, `internal/sqlconsole/pools.go:501-525`; supporting view policy in `internal/schema/sql_view_inspect.go:76-133`.

The analyzer records function, cast, and operator names, but plan validation ignores those fields and trusts catalog relations. Existing SQL-view inspection permits any nonvolatile `pg_catalog` function/operator/type, whereas N8 requires a smaller exact builtin OID allowlist and transitive view inspection. A managed SQL view can therefore contain a stable `pg_catalog` function outside N8's allowlist; a console query of that view exposes only the relation to N8 and bypasses the missing dependency check.

A focused PG17/18 fixture should create a valid managed view containing a nonvolatile builtin excluded from `allowedFunctions`, then prove console read rejects its resolved OID before execution. Operator, implicit-cast, aggregate/window, and nested-view variants must fail closed as well.

### N8-R9 — P2 — configured write columns are replaced by all physical columns

**Path:** `internal/sqlconsole/pools.go:221-255`, `internal/sqlconsole/pools.go:515-523`.

Target inspection stores every physical column in `targetSnapshot.Columns`; plan validation later treats that map as the configured write-column allowlist. Exact database grants supply a second rejection layer, but AST/preflight accepts an unconfigured column and delegates the policy to PostgreSQL.

A focused fixture should configure and grant one of several physical columns, then prove `ValidatePlan` rejects an update of an unconfigured column before runner execution.

### N8-R10 — P2 — direct read API accepts a multi-statement array

**Path:** `internal/sqlconsole/types.go:197-201`.

Request normalization permits up to ten statement elements in both modes. The source N8.1 contract assigns a single `SELECT`/`VALUES` to read mode and the maximum-ten array to transactional write mode. UI commit `485d9af` enforces the rule only in the browser; direct API callers can still submit several read statements.

A focused unit/API test should reject two read statement elements while retaining multi-statement write transactions.

### N8-R11 — P1 — persisted pending writes cross administrator sessions

**Path:** `ui/admin/src/pages/sql-console-page.tsx` pending-write storage and reconciliation added by `8df8757`; shared App/session composition.

The single `pgbase.sql-console.pending-write/v1` session-storage key contains the full raw SQL and parameters and survives logout, expiry, and a later login in the same browser tab. If administrator A leaves an unresolved write and administrator B logs in, B loads A's attempt. Actor-scoped GET correctly returns not-found, after which the page replays A's DML under B's authenticated session.

This violates same-session ownership and crosses raw SQL/parameter and mutation intent between administrators. A focused composition regression must persist A's attempt, transition through explicit logout and expired-session login to distinct administrator B, and prove B cannot inspect or replay it. Persistence must be bound to the authenticated session or cleared on every session transition while preserving same-session reload recovery.

Worker commit `cd092fb` closes N8-R11 by keying recovery under an optional opaque session capability, refusing persistence without it, and deleting the legacy global key unread. It also closes the R6 composition follow-up by detecting durable summary DTOs whose statement rows are omitted and labeling them expired or unavailable while retaining commit/row/byte metadata. The independent focused adapter/page run passed all 17 tests. Final composition must verify that the backend capability is non-secret, changes across distinct/expired sessions, and remains stable across same-session reload.

### N8-R12 — P2 — retention and no-replay policy has no implementation

**Path:** `migrations/system/000041_sql_console.sql:56-91`, `internal/sqlconsole/ledger.go`.

The migration creates cleanup indexes, but no retention duration or cleanup path exists, so full execution and receipt rows grow indefinitely. Deleting both rows naïvely removes the unique administrator/idempotency digest and permits the old mutation to run again.

The clarified acceptance is bounded cleanup of heavy execution/history/result data with an indefinitely retained minimal redacted idempotency tombstone. The total tombstone key count may remain globally unbounded if that storage tradeoff is documented. Closure must validate foreign-key/deletion order and prove that an aged same-actor/key/body replay cannot execute another mutation after cleanup.

## Standards review

The candidate is clean under `git diff --check`, `gofmt -d`, and `go vet` for the changed backend packages. Changed production modules stay below the repository's 800-line ceiling. Naming and package boundaries are consistent with nearby code, and the code uses structured errors rather than panics for request failures.

The principal maintainability concern is that policy snapshots and durable state transitions are distributed across `Pools`, `Runner`, `Service`, and `Ledger` without one owner for freshness or transition invariants. N8-R1 and N8-R4 are concrete consequences, so they are recorded as product defects rather than style observations.

One additional bounded candidate remains for fix-delta review: `inspectWriteTargets` stores every physical column in `targetSnapshot.Columns` at `internal/sqlconsole/pools.go:221-255`, while `ValidatePlan` treats that map as the configured write-column allowlist at `internal/sqlconsole/pools.go:515-523`. Exact database grants currently provide a second rejection layer, but the requested pre-execution configured-column guard appears to be lost. This needs a focused fixture before severity is finalized.

## Spec review

The reviewed implementation otherwise has the expected core shape: read transactions are read-only and rolled back; writes use one transaction and one commit after caps; dry-run, statement errors, and cancellation roll back; cancellation is sent only for the borrowed connection and that connection is discarded; parameters use extended-protocol bindings; result cells preserve explicit null/numeric/JSON/bytes/temporal tags; raw SQL, parameters, and results are absent from durable rows; API ownership is actor/session scoped; and startup role checks reject broad role, schema, relation, and receipt privileges.

The following required evidence is missing and is not counted as an additional product defect yet:

- Full PG17 and PG18 startup-role, side-effect target, transaction, cap, cancel/discard, receipt, and restart matrix. The repository tests require `PGBASE_TEST_ADMIN_URL`; no isolated URLs have been authorized yet.
- Browser evidence for the independent SQL page, cancellation recovery, exact-value copy, write targets, multi-statement outcomes, and unknown final state. UI was intentionally outside this backend pass.
- A fixed durable history/receipt retention policy and a no-mutation-replay proof after retention. `N8.md` says the policy must be set before migration, but the current migration only supplies cleanup indexes.

The full execution plan's single-statement read-mode requirement is now enforced by UI commit `485d9af`; backend request normalization still accepts up to ten read statements, so direct API conformance remains open. The result model also exposes `Truncated`, while the runner currently hard-fails the row/byte cap rather than producing a truncated result. The latter remains a contract/evidence gap until the lead resolves the intended behavior.

## Verification evidence

Passed against `10d9df4` without a database:

- `go test ./internal/sqlconsole -run 'TestAnalyze|TestNormalize|TestRequest|TestParameter|TestCell|TestClone|TestCanonical' -count=1`
- `go test -race ./internal/sqlconsole -run 'TestAnalyze|TestNormalize|TestRequest|TestParameter|TestCell|TestClone|TestCanonical' -count=1`
- `go test ./internal/api -run '^TestSQLConsole' -count=1`
- `go test ./pgbase -run '^TestSQLConsoleOptions' -count=1`
- `go vet ./internal/sqlconsole ./internal/api ./pgbase ./migrations/system`
- `git diff --check 91a0bdac...10d9df4`
- `gofmt -d` on the N8 Go delta
- `npm test -- src/backend/sql-console-backend.test.ts src/pages/sql-console-page.test.tsx src/app.test.tsx src/backend/http-backend.test.ts` at integration `485d9af` (66 tests)
- `go test ./internal/api ./pgbase -run 'SQLConsole|SessionResponse' -count=1` at integration `485d9af`
- `git diff --check 5e2ed00^..485d9af`
- `npm test -- src/backend/sql-console-backend.test.ts src/pages/sql-console-page.test.tsx` at integration `c90412c` (11 tests)

The broad package test compiled and passed `internal/api` and `pgbase`; database integration tests in `internal/sqlconsole` and `migrations/system` stopped because `PGBASE_TEST_ADMIN_URL` is unset. That is an environment evidence gap, not a test failure attributed to the candidate.

No database was started or mutated. Ports `8090`, `55440`, and `59000` and all user data remain untouched. Euclid reserved project `pgbase-n8-20260910` with PG17/18 ports `55470`/`55471`; this review will wait for explicit fixture URLs and coordination before mutating checks. API port `58096` is also reserved for the later browser/API pass.

## Remaining handoff

Euclid and Linnaeus received N8-R1 through N8-R4 and N8-R8 through N8-R10 plus N8-R12 with path, line, contract, and focused regression requirements. Feynman and Linnaeus received N8-R5 through N8-R7, the N8-R5 follow-up, and N8-R11; those UI findings are closed at `35712f4` pending final capability composition. Backend closure requires stable owner fix SHAs plus the authorized PG17/18 evidence. Final same-reviewer closure reviews only those backend and UI fix deltas plus any later lead-composition delta; unchanged backend and shared/UI code will not be re-reviewed.
