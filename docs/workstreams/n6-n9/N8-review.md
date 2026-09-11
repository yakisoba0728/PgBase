# N8 independent Standards and Spec review

## Current versus historical checkpoints

The verdict at the top is the current status for the stated final candidate. The R1–R12 entries below are historical checkpoint narratives; their earlier `open`, `pending`, or `reopened` labels apply only to the commit named by that checkpoint. A parser looking for current findings should use the top verdict and the explicit `Current remaining findings` field, while retaining the checkpoint text for audit history.

Current remaining findings: **0**.

## Final independent Standards and Spec verdict

**PASS for the agreed full local N8 review scope at `b7674f704e14b07104ebf6f1c9ee5e53092f367e`** on the lead's `codex/n6-n9-final-candidate`. Standards and Spec review have no remaining actionable findings. N8-R1–R12, the R12 replay-pool follow-up, the final R4 terminal-publication regression, backend session-identity composition, and the presentation overflow finding are closed.

This verdict combines the original bounded backend review, only the subsequent unresolved fix/composition deltas, the independently reproduced R8 failure, meaningful owner PostgreSQL evidence, and the lead's accepted real-browser evidence. Unchanged backend/UI scopes were not reviewed again. It is a local candidate review, not a deployment or a claim that the explicitly excluded supplemental browser scenarios ran.

- Final backend product candidate: `752d38c800eeee66cfa61f0fb18f1d42c9a533a6`, following `0e4ba42df129c4b3e5d849e359ec8c5019a27e3a` and `4d773b5d90ec2bd6f9201e5a87086a8d831fd150`.
- Shared callback/capability composition: `66fec8fa8dc6f543b36183ba75873d4a9c073162`.
- Closed UI source: `cd092fb41859812dc333774d4522586f22642757`, integrated as `35712f45be41f8239383824c55401727d510e0ed`, with earlier reviewed fixes listed below. No closed UI scope was repeated.
- Backend contract/evidence documentation reviewed: `2558d199408284fe27202229cdcce5b3890f2f4a`.
- Session-conformance expectation-only composition reviewed: `e57d233674f46120e5fa01fc3446fbf7a4e60ea5`.
- Final private replay-helper cleanup: `d95848d59591309be886f6cbc16d86c2efbdee96`.
- Final terminal-publication correction and embedded UI bundle: `b7674f704e14b07104ebf6f1c9ee5e53092f367e`.
- Presentation fix: worker `5235d9e03b5e6530e3049fdf00586e855b0c8ba8`, integration `882a519436725e6e91843c19dea3a307fb2ccf42`, embedded bundle `index-CTAdrZsH.js`.
- Remaining findings or required review work within the agreed local N8 scope: **none**. Lead retains integration/release ownership.

| Finding | Final closure basis |
| --- | --- |
| R1 catalog freshness | Version gate at `4d773b5`, durable schema-state callback at `66fec8f`, owner PG regression. |
| R2 commit unknown | Receipt-absent transport error remains unknown with nullable commit state; runner/service/ledger evidence at `4d773b5` and the full PG runs. |
| R3 virtual fields | File and multiple-relation fields excluded from physical-column expectations; PG startup regression. |
| R4 durable coordination/publication | `0e4ba42` gates replacement startup against actual in-flight transactions; `b7674f7` persists terminal state before publishing it and consistently surfaces persistence errors. |
| R5 write identity | Exact replay/reload recovery at `8df8757`, scoped recovery at `cd092fb`, actual response-loss/pre-admission browser cases. |
| R6 history/results | Real execution GET and durable-summary handling at `16ce11f`/`cd092fb`; focused tests and actual browser Inspect. |
| R7 configured timeouts | Untouched override omitted; configured API capabilities at `4d773b5`/`66fec8f`; actual 1500/2000 ms browser fixture. |
| R8 transitive dependencies | Independent custom enum IO repro at `4d773b5`; fail-closed transitive deparse/AST plus native checks at `0e4ba42`; rejected enum and accepted builtin IO controls. |
| R9 exact write columns | Separate configured and physical column maps at `4d773b5`; rejected-column preflight regression. |
| R10 single read | Backend normalization at `4d773b5` and the already closed UI rule. |
| R11 session isolation | Scoped optional opaque capability at `cd092fb`/`4d773b5`/`66fec8f`; actual logout/login and expiry browser cases. |
| R12 retention/no replay | Permanent accepted identities, atomic bounded cleanup and retained outcomes at `0e4ba42`; released replay admission connection at `752d38c`; semantics preserved at `d95848d`. |

Final R4 closure evidence: the lead's deterministic row-lock test failed before the correction (0.806s), exposing local `Succeeded` while `Ledger.Finish` was blocked. After local-copy → persist → publish, the five affected PostgreSQL race cases passed (3.439s): `TestServicePublishesTerminalOnlyAfterLedgerFinish`, `TestServiceSurfacesTerminalLedgerFailure`, `TestLedgerConcurrentReplaysReleaseAdmissionConnection`, `TestServiceNeverReplaysExpiredWriteWithoutReceipt`, and `TestServicePersistsRejectReplayResultExpiryAndActualCancel`. The reviewed source keeps GET/Cancel/replay nonterminal while persistence waits, returns the persistence error consistently on failure, and preserves the decided successful outcome if cancel arrives during persistence.

The lead confirmed the exact final five-case run used `GOTOOLCHAIN=go1.26.8 GOMAXPROCS=2 go test -tags=integration -race ./internal/sqlconsole -run 'Test(ServicePublishesTerminalOnlyAfterLedgerFinish|ServiceSurfacesTerminalLedgerFailure|LedgerConcurrentReplaysReleaseAdmissionConnection|ServiceNeverReplaysExpiredWriteWithoutReceipt|ServicePersistsRejectReplayResultExpiryAndActualCancel)$' -count=1`, with its own isolated fixture supplied through `PGBASE_TEST_ADMIN_URL` (port 55472). Two retained logs were inspected during that task, but they lived under `/tmp` and are not durable repository evidence; the reviewer did not connect to that fixture or repeat the run.

Final presentation closure: reviewed the three-line markup change, existing overflow CSS, rebuilt bundle, and a task-local 1440 px screenshot. The screenshot is not retained in this clone, so it is recorded as historical unavailable evidence rather than a durable link. The result viewport stayed within the shell; horizontal scrolling contained the wide row and label/copy spacing was readable. Owner evidence: focused component 14/14 and only the affected actual exact-read/clipboard case passed. Lead's final typecheck/Vite build passed.

Final Standards evidence: independent `go run ./tools/repostats -rev b7674f704e14b07104ebf6f1c9ee5e53092f367e` confirmed Go 69,323, UI 6,667 and handwritten 75,990 lines, 67 interfaces, 1,251 exported Go declarations, 55 constructors, 154 UI page state/effect calls, 229 UI exports, and zero production files over 800 lines. The exact measured ceilings, unchanged classifier, immutable migrations 1–40, verified migration-41 checksum, and reviewed scoped rationale match the final composition. `git diff --check` passed for the final correction.

Evidence limits remain explicit: the 13 accepted real-browser scenarios passed, with raw runs and focused harness reruns preserved in `N8-browser.md`. Supplemental actual API-restart, forced commit-unknown rendering and wall-clock TTL browser scenarios did not run to completion and are not claimed here. The lead accepted the combined backend recovery and existing UI source/unit evidence for those mechanisms. The permanent minimal identity/receipt key count can grow indefinitely, as documented for exact no-replay.

Reviewer process/resource state at closure: no reviewer-owned process remains running. The only reviewer database mutation was the owner-authorized, named `pgbase_n8_review_r8` reproduction on PG17 port 55470; that database was dropped on test exit. No reviewer roles were created. Live ports 8090/55440/59000 and user data were not touched. No additional agents or automations were created.

At `752d38c`, both duplicate/conflict paths release the admission connection before nested pool lookups. The owner synchronized eight concurrent replays at the minimum four-connection pool, observed the prior timeout, then verified completion with no new admission and zero acquired-connection leaks after the fix. The focused replay/retention batch passed PG17 with race detection (2.741s) and PG18 (1.621s); focused SQL-console lint reported zero issues. Final migration-41 tests passed PG17 (0.432s) and PG18 (0.439s). Those results, together with the reviewed test sources and prior full-package evidence, close R12 without repeating the entire matrix.

The historical checkpoints and findings below preserve how each defect was identified and closed; their earlier open/pending labels apply only to the stated earlier commits.

Composition `d4fb6a201194e01509e384abb49a469066fc32de` was checked against the reviewed backend/UI pins: the SQL-console/API/options production tree matches `752d38c`, and UI runtime source matches `35712f4`. The later conformance test adds the expected `sqlConsoleEnabled:false` field. The updated embedded bundle contains the reviewed scoped recovery and session/timeout capability code. Migration 41 SHA-256 is `628b3920dd97db4b419ba7f2ed4b3b921911a07d374033ad8e2f0a849871d699`; migrations 1–40 remain unchanged from `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`.

Independent `go run ./tools/repostats -rev d4fb6a201194e01509e384abb49a469066fc32de` exactly confirmed the documented ceilings: Go 69,292; UI 6,667; handwritten 75,959; interfaces 67; exported Go declarations 1,251; constructors 55; UI page state/effect calls 154; UI exports 229; zero production files over 800 lines. The unchanged classifier, fixed per-file cap and documented approved-feature scope support this budget update. Only the exact later N6 mechanical delta and detailed browser evidence remain for full local closure.

At `17ced7e83125d3f2301451154c84549e27993edb`, the six-file N6 cleanup adds exactly 17 runtime Go lines and does not change N8 production code. Final ceilings are Go 69,309 and handwritten 75,976; the other metrics and fixed per-file cap remain unchanged. N8 PostgreSQL tests were moved under the repository's `integration` build tag without changing their assertions. Later watch-buffer synchronization (`433a753`) and release-rewind inclusion of the final identity table (`a31975f9c1ad01b413d8f72a2c9d0b5fad1d7142`) are test-only corrections with focused race passes supplied by the lead.

The lead-preserved browser record at `eed4ce9:docs/workstreams/n6-n9/N8-browser.md` supplies 13 real Chromium/API/PG17 scenarios at source `e57d233674f46120e5fa01fc3446fbf7a4e60ea5`: default/timeout/exact-value copy, typed decimal parameters, CSRF, multi-statement commit, dry-run, later-statement rollback, forbidden targets, cancel/poll/next query, durable history inspection, lost accepted response, loss before admission with exact replay, cross-login/expiry session isolation, and result-cap rejection. Nine initial passes plus four affected harness-only reruns constitute that evidence; no fabricated response payload or full-suite repetition was claimed. Backend `752d38c` changes no UI/API/schema/grants, so its focused replay evidence composes with these browser runs.

The browser handoff explicitly excludes unfinished supplemental API-restart, forced commit-unknown, and wall-clock TTL scenarios. The lead accepted the 13-scenario browser scope and relies on the closed backend recovery tests and UI source/unit evidence for those mechanisms. This review preserves that limitation and does not claim those additional browser scenarios ran.

Presentation-only worker delta `5235d9e03b5e6530e3049fdf00586e855b0c8ba8` adds the existing `table-scroll` overflow wrapper and inline spacing between result text/type/copy controls. Its source and existing CSS were reviewed with no functional finding; final integrated bundle and screenshot closure remain pending.

Private helper-return cleanup `d95848d59591309be886f6cbc16d86c2efbdee96` preserves behavior: every removed bool was false, both public admission return paths still report `created=false`, and early rollback/connection release remains before nested lookup. It adds two Go lines, taking final measured ceilings to Go 69,311 and handwritten 75,978 before any subsequent correction.

**R4 reopened by concrete final evidence:** the lead's affected PG replay run at `d95848d` observed `waitForStatus(Succeeded)` followed by a forced in-memory TTL expiry returning durable `Running` with no result. `finishWithoutRun` publishes `job.execution` terminal before calling `Ledger.Finish`, so the API can expose completion before durable state exists (and before a potential persistence error). The lead is preparing only a local-snapshot → persist → publish correction and a deterministic row-lock regression proving that polling remains nonterminal while persistence is blocked and exposes matching terminal state only after it succeeds. This invalidates the earlier R4 terminal-durability closure; it does not reopen unrelated scopes or the helper-return refactor.

## Resumed fix-delta review — 2026-09-10

The user resumed review from checkpoint `9ac351ab504dcc683bc5139bf0e01db217286be5`, with speed priority and no duplicate review of closed scopes. The candidate is owner `4d773b5d90ec2bd6f9201e5a87086a8d831fd150`, imported unchanged into this review branch as `0687a95`. The reviewed delta is `10d9df4..4d773b5`; the paused record below is historical.

Focused independent no-database checks passed: `go test ./internal/api ./internal/sqlconsole ./pgbase -run '^(TestSQLConsole|TestNormalize|TestRequest)' -count=1`.

R1, R3, R9, R10 and the backend session capability have source corrections, with final callback/capability composition still to be checked. R2 now preserves a receipt-absent transport commit error as unknown, and its tests assert nullable commit state. R4 now rejects a normal second service startup using an advisory lock and surfaces terminal-ledger failures. Owner PG evidence will be reused; this reviewer will independently reproduce only concrete remaining concerns.

The bounded residual batch sent to Euclid is:

- **R12 confirmed residual:** `internal/sqlconsole/ledger.go:234-236` and `:276-277` delete aged unknown/failed/canceled write executions without retaining their identity unless a success receipt exists. Exact replay then becomes a new execution. Euclid confirmed this and is implementing permanent minimal reservations for every accepted write. Closure requires aged no-receipt unknown and failed replay to preserve identity/status and leave executor invocation counts unchanged, plus concurrent same-ID/key coverage.
- **R8 confirmed residual:** `internal/sqlconsole/read_guard.go:127-189` scans only named function/operator IDs. A managed view casting safe text through a custom enum and back to text contains `CoerceViaIO` type OIDs outside that scan. The focused independent PG17 repro at `4d773b5` failed its rejection assertion: `inspectManagedReadRelations` accepted `(visible::ext.review_enum)::text` despite the custom type being outside `allowedCasts`. The test explicitly verified `COERCEVIAIO` in `ev_action`. Euclid authorized only named fixture `pgbase_n8_review_r8` on `55470`; it was created and dropped during the 0.05-second test, with no roles or other databases touched. Repro source is archived in `N8-R8-CoerceViaIO-repro.go.txt`. Closure requires rejecting that concrete custom-type cast before execution, alongside the already supplied managed-view checks.
- **R4 focused concern awaiting reproduction:** `internal/sqlconsole/ledger.go:62-82` holds an idle coordinator connection; the service has no loss/fencing check. If only that backend dies, service A can retain running work while service B acquires the released lock and reconciles it. Closure requires an isolated coordinator-loss/blocked-write regression proving that committed work cannot be durably labeled uncommitted and the unfenced service cannot admit new work.

No full-pass verdict is issued at this stage. Closed R5–R7/R11 and the early parser/clone correction remain closed. Final review is limited to these unresolved fixes and the final stable lead-composition delta, using supplied PG/browser evidence.

The lead clarified the cap contract during this delta review: exceeding affected/result caps is a hard failure with rollback before write commit. Admitted results may retain `truncated=false`; no truncation feature is required. The earlier cap contract question is closed.

Shared constructor/callback delta `66fec8fa8dc6f543b36183ba75873d4a9c073162` was reviewed across all three changed files, plus the required store/UI endpoints. It reads durable `pgbase_meta.schema_state` through `schema.NewStore(control).SchemaVersion` and supplies configured handler targets/default/maximum timeouts. The purpose-separated HMAC session identity matches the closed UI scope contract. No additional finding was found in this composition delta. Lead-supplied focused API/pgbase/OpenAPI checks passed; no closed UI tests were repeated. Final owner fixes and final budget/dist/browser evidence remain pending.

Residual candidate `0e4ba42df129c4b3e5d849e359ec8c5019a27e3a` was reviewed only as a delta from `4d773b5` and imported unchanged as `92d2ecf`.

- R4's real runner now holds a shared transaction execution gate before checking coordinator ownership and before user SQL. Replacement startup holds the exclusive gate while reconciling, so an old transaction must settle before receipt/status reconciliation. The focused actual-write/coordinator-termination/replacement regression passed. This closes R4's residual.
- R8 now applies the fail-closed analyzer to each transitively visited deparsed view in addition to native dependency inspection, and explicitly rejects native `SQLVALUEFUNCTION`. The owner regression rejects the independently reproduced custom enum IO cast and accepts the builtin UUID/text IO control. This closes R8's residual.
- R12 now atomically reserves every accepted write identity, retains minimal terminal outcomes before deleting heavy rows in batches of 500, and returns the retained status or receipt on replay. Owner public-service regressions cover aged unknown/failed/canceled/timed-out writes, restart, unchanged executor count, and changed-body conflicts. One new availability regression remains below.

**R12 follow-up — P2 — nested control-pool borrowing during duplicate replay:** at `0e4ba42`, `internal/sqlconsole/ledger.go:159-161` and `:182-183` roll back their admission transaction but retain the borrowed connection until return. They then call `retainedWrite`/`existing`, which borrow from the same control pool again. Concurrent duplicate requests equal to the pool capacity can hold every slot while waiting for a second slot, starving control operations until cancellation. Euclid received the exact paths and closure condition: release the admission connection before nested lookup (or consistently reuse one connection), and prove synchronized concurrent replays complete with one execution and no acquired-connection leak at the minimum four-connection pool.

Owner evidence reused at `0e4ba42`:

- PG17 `go test -race ./internal/sqlconsole -count=1`: PASS, 8.526s.
- PG18 `go test ./internal/sqlconsole -count=1`: PASS, 6.982s.
- `go test -race ./internal/api ./pgbase -run '^TestSQLConsole' -count=1`: PASS, 1.422s/1.466s.
- These full package runs include the focused retention, coordinator-loss and transitive-view regression sources reviewed above. Final migration and final composition/browser evidence remain pending.

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
