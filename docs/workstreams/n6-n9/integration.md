# N6–N9 integration

Branch: `codex/n6-n9-integration`

Baseline: installed-status `117a1db217f2054440529d54a80dab7f58b35d6e`, merged product `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`

Plan commits: `133f6448b598d4df706ffbf691f4160a816f385b`, `8dadf42`

Owner: GPT-5.6 Sol integration
Last updated: 2026-09-10 04:24 KST

## Current milestone

G0 and G1 are complete. G2 N6/N7 implementation is active. N6's domain-service, hook and migration slice passed its first isolated PostgreSQL application gate. N7's current product diff has no remaining Newton finding and its actual Flutter browser gate passed; the actual failure matrix and same-origin admin browser remain. G3 is a partial integration state, N8/N9 product work is gated, and no final acceptance is claimed.

The branch was created directly from `117a1db`. The two docs-only plans were cherry-picked as `7cedc6e` and `ff3dada`. N6 product handoff must skip worker plan `9a30b2f`; N7 product handoff must skip rebased plan `39b170c`. Only stable product/test/docs commits after those plan commits are eligible.

| Workstream | Worker / branch / worktree | State at 03:38 KST | Integration requirement |
| --- | --- | --- | --- |
| N6 | Chandrasekhar, Sol / `codex/n6-jsvm` / `PgBase-worktrees/n6-n9/n6` | domain services/hooks/migrations integrated through `94b32b3`; focused race and isolated PG exact 3-test green, no blocker | durable jobs, broader migrations, CLI, external JS app, stress and review |
| N7 | Einstein, Terra / `codex/n7-dart-sdk` / `PgBase-worktrees/n6-n9/n7` | Standards 0/Spec 0; actual `58091` stale ETag, idempotency replay/conflict, cursor exact JSONB and batch rollback added through `d0deb9d` | revocation/SSE, admin 401/403, cursor-expired/cancel upload, same-origin admin browser |
| N8 | Euclid, Sol `01a0879d-2152-7770-89da-2db5b4e51cc9` / `codex/n8-sql-console` / `PgBase-worktrees/n6-n9/n8` | active pre-G3 preparation, `N8.md` only; no product/deps/DB changes | wait after seam inventory; product start only on G3 signal |
| N9 | Sagan, Terra `01a0879d-21cf-73a0-8762-3db86df2f9f6` / `codex/n9-self-update` / `PgBase-worktrees/n6-n9/n9` | active pre-G3 preparation, `N9.md` only; no product/deps/DB changes | wait after seam inventory; product start only on G3 signal |
| Integration | GPT-5.6 Sol / this branch | board and plan base ready; N7 404 diagnosed | stable cherry-picks, conflict/API review, cross-feature and final gates, shared docs and final clean commit |

## Ownership and invariants

N6 owns `internal/jsvm`, JS public facade/options/CLI/hooks and required API commons. N7 owns `sdk/dart`, its scripts, examples and N7 docs. Workers do not overwrite each other. The integration lead owns common composition, route/OpenAPI/generated files, `go.mod`/`go.sum` resolution and all shared status documents: `TODO.md`, `docs/next-work.md`, `docs/project-status.md`, `docs/pocketbase-current-reassessment.md`, and this file.

System migrations 1–40 are immutable. N6/N7 currently require no new system migration. If N8 or N9 proves a durable schema requirement, this document allocates 41 or later before code is written. Product Go files retain the 800-line limit; final global budget movement requires measured feature delta and independent rationale.

No N6–N9 test may target the installed PostgreSQL 17.9 on `55440`, MinIO on `59000`, HTTP on `8090`, its binary, Docker resources or userdata. Completed N1–N5 install/rollback validation is historical evidence and will not be rerun.

## Isolated service allocation

| Owner | Docker project / resources | Loopback ports | State |
| --- | --- | --- | --- |
| N7 | `pgbase-n7`; PostgreSQL and PgBase containers; task-owned local file directory/volume; repo-external Flutter web | PostgreSQL `55441`, API `58091`, Flutter `58092` | active through browser/review closure; file route fixed |
| N6 | `pgbase-n6-pg-20260910`; pinned PostgreSQL 17.9 | PostgreSQL `55469` | active for worker integration tests; no N6 app/MinIO port allocated yet |
| Integration | `pgbase-n6n9-integration` or another recorded unique name | unallocated; check running ports first | not provisioned |
| N8/N9 | each worker owns a distinct project/name/port row before startup | unallocated | gated |

Port and container allocation is written here before starting a new fixture. A worker cleans only resources it created and names explicitly.

## Acceptance gates

- G2 N6: stable product commits, exact public Go/JS API inventory, dependency rationale, focused unit/race and isolated actual HTTP/PG evidence, external module and JS application.
- G2 N7: stable product commits, Dart analyze/test, external Dart and Flutter consumers, actual file/SSE/admin HTTP, browser-driven Flutter flow, cancellation/auth-generation/exact-number contracts.
- Independent review: Standards and Spec remain separate against merge base `2939f15`, the N6–N9 plan and each stream status document. Reproducible contract findings return to the owning worker before integration acceptance.
- G3: integrate stable N6/N7 commits, resolve only common seams, verify Go JS facade and Dart/Flutter consume the same route/auth/ETag/idempotency/exact-number/SSE/file contract.
- G4: request Astra to assign N8 Sol and N9 Terra only after G3 core evidence is green. Preparation research may be separately scoped; product implementation remains gated.
- G5: focused failures first, then final all-package Go unit/race, isolated PostgreSQL/MinIO integration/race and skip policy, CLI, browser, Dart/Flutter/native fixture, lint/vet, OpenAPI and generated reproducibility, structural measurement, six supported builds. A passed gate is not repeated unless relevant code changes.

No public release, npm/pub.dev publication, cloud deployment, remote merge or replacement of the installed live service is in scope.

## Phase log

- 2026-09-10 03:31 KST — confirmed clean `codex/n1-n5-installed-status` at `117a1db`, created `codex/n6-n9-integration` from that exact commit, and applied docs-only plans. Integration commits are `7cedc6e` and `ff3dada`; no product code, migration or live service changed.
- 2026-09-10 03:35 KST — inspected both assigned worktrees and task status. N6 was dirty WIP on plan commit `9a30b2f`. N7 had stable implementation `11923c6` plus hardening in progress; Docker inventory showed only N7 `55441/58091` and the excluded live PostgreSQL `55440`.
- 2026-09-10 03:36 KST — N7 finished clean hardening commit `ed6fcde` and handed off Dart analyze, seven unit/wire tests, external CLI and Flutter widget-test evidence. Actual file upload remained the only reported fixture blocker; no Go change was made.
- 2026-09-10 03:37 KST — reran the N7 live command against its own `58091` server. It deterministically exited 255 at `Files.upload` with `PgBaseError(404, not_found)` after schema/auth/CRUD/batch/SSE passed. The command did not use live ports or reveal credentials.
- 2026-09-10 03:38 KST — diagnosed the 404 as missing fixture file configuration. An unauthenticated POST to the file route returned generic router 404 rather than an auth decision; the container environment had no `PGBASE_FILES_DRIVER` or `PGBASE_FILES_LOCAL_DIRECTORY`; `withFileServices` therefore left `dependencies.Files` nil and `fileRoutes` unregistered. Returned a narrow N7 fixture-only fix; SDK/server semantics remained unchanged.
- 2026-09-10 03:43 KST — integrated N7 stable product/hardening commits as `cae25dc` and `2c97f31`, excluding rebased plan `39b170c`. Integrated N6 foundation `218f276` as `15676e5`, excluding worker plan `9a30b2f`. N6 focused race passed on the combined integration branch for `internal/jsvm`, `pgbase`, and `internal/api`.
- 2026-09-10 03:44 KST — integrated N7 `52e23ce` as `85c249a`. Its isolated local-storage fixture passed user upload/list/token/byte-exact download/delete and admin upload; the worker also passed Dart analyze, seven tests, external CLI and Flutter widget tests. On the integrated commit, Dart analyze and all seven tests passed again. Actual Flutter browser and independent review remain open, so N7 is not complete.
- 2026-09-10 03:47 KST — integration lead reran the original N7 live conformance after `85c249a`; it exited 0 through schema/auth/CRUD/ETag/batch/SSE and full file round trip on dedicated `58091/55441`. Newton/Sol task `01a0877e-6229-7df1-be93-e66ac50c6ffb` is the sole independent N7 Standards+Spec reviewer; no duplicate review worker will be created. Einstein is preparing an external Flutter browser flow on N7-owned HTTP `58092`, including an N7-only service restart for SSE replay.
- 2026-09-10 03:55 KST — integration lead drove a separate in-app browser against the repo-external Flutter consumer on `58092`. Visible assertions passed for login/SSE connection, create/update with a strong ETag, exact `900719925474099312345.0001`, browser file upload/download/delete and SSE create delivery. The tab retained cursor 23 across the N7-owned PgBase container restart and received events 24–27 without reload or re-login; browser warning/error log was empty. Worker commit and Newton review remain pending.
- 2026-09-10 03:56 KST — N6 reported no blocker after foundation. Current WIP is the admitted-request public Go facade for real records/mail/files, exact raw JSON DTO, shared bridge, lifecycle priority/veto/paired After adapter and jobs. The next stable gate is isolated PostgreSQL with real domain service, authorization/context-forgery rejection and hook ordering/outcomes; migrations, CLI, external JS app and stress remain afterward.
- 2026-09-10 03:56 KST — Newton's independent N7 review reproduced P1 stale-token persistence when delayed storage write completes after clear/logout, non-canceling stalled upload hashing, and an old admin request's late 401 clearing a newer login's CSRF state; P2 retry of terminal SSE 410/400/401/403 and realm profile envelope mismatch. It also found the typed approved schema/import plan snapshot with stable replay IDs and server request ID propagation absent, plus live-contract coverage gaps. Findings were returned to Einstein with exact narrow fixes/tests; Newton remains assigned through re-review.
- 2026-09-10 04:00 KST — integrated Newton's review-only `7fbe383` as `0128a9b`. `N7-review.md` records Standards 0 and Spec 7: three reproduced P1 findings and four P2 findings including approved plan/replay binding and structured request-ID/network errors. The review changed no product file and used no shared/live fixture. Einstein is implementing the returned fixes; Newton remains the sole reviewer for focused closure.
- 2026-09-10 04:03 KST — observed N6's first dedicated database fixture: container `pgbase-n6-pg-20260910`, pinned `postgres:17.9-bookworm` image, loopback `55469`. It does not collide with N7 or installed ports. Worker WIP now includes JS migrations and an integration test, but no stable commit or PG pass has been reported; the gate remains active.
- 2026-09-10 04:05 KST — integrated N7 owner fixes `2bd50f2` as `5c218ab` and acceptance evidence `c266bc5` as `fb5111c`, without rebased plan `39b170c`. The changes address all seven review topics and add the browser fixture. On the integrated branch, Dart 3.13.3 analyze and all 17 focused tests passed. Newton received the exact fix SHA for reproduction-based re-review; N7 remains open until that closure and remaining live-contract coverage are resolved.
- 2026-09-10 04:08 KST — Newton re-review confirmed the prior 17 tests/analyze and closed stale admin 401, stalled file cancellation, terminal SSE classification, realm profile, request ID and save-vs-clear behavior. Exact repros still fail for clear-started-before-restore ordering, mutation of a nested approved-plan payload during/after preview, and unmapped `http.ClientException` network failures. Returned those one P1/two P2 items to Einstein; no N7 completion is claimed.
- 2026-09-10 04:17 KST — integrated N6 domain-service/hooks/migrations `712900b` as `94b32b3`. Combined focused race passed for `internal/jsvm`, `pgbase`, and `internal/api`; the integration lead independently reran the dedicated PG17.9 `55469` exact three-test gate and passed real file/SMTP, migration ledger/rollback/Down, and record authorization/hook/idempotency contracts. Durable jobs, broader migration/CLI/external-app/stress/review work remains.
- 2026-09-10 04:17 KST — integrated N7 second fixes `5983a93` as `2ccdc69` and SSE body-network fix `8ccd9f9` as `46c1930`. Newton's exact re-review closed clear-before-restore, approved-plan immutability, send-time and body-stream network error contracts; Dart analyze and 20 tests passed in the owner/reviewer fixtures. Product review findings are now zero. Actual `58091` failure matrix and same-origin admin browser remain before N7 core acceptance.
- 2026-09-10 04:17 KST — optional pre-G3 preparation can be assigned without product work: N8 Sol may inventory the existing admin session/origin/CSRF chain and PG parser/dependency/cancel seams into N8 status only; N9 Terra may inventory six-build artifacts, manifest/signature inputs, and platform atomic replacement/rollback seams into N9 status only. Shared docs, migrations and product code remain untouched until the G3 start signal.
- 2026-09-10 04:20 KST — integrated Newton's final review-only `4c19f7f` as `d4598ff`; bounded N7 result is Standards 0 and Spec 0. On the exact integrated source exported to a disposable archive, dependency resolution, Dart analyze, and all 20 tests passed. The first archive attempt had placed `PUB_CACHE` inside the analyzed tree and was discarded as a fixture error; the corrected cache was outside the source tree.
- 2026-09-10 04:20 KST — Astra assigned pre-G3 preparation to Euclid/Sol (`01a0879d-2152-7770-89da-2db5b4e51cc9`, `codex/n8-sql-console`, N8 worktree) and Sagan/Terra (`01a0879d-21cf-73a0-8762-3db86df2f9f6`, `codex/n9-self-update`, N9 worktree). Both tasks are active and restricted to their single status document and seam inventory; no product, dependency or database change is authorized before G3.
- 2026-09-10 04:24 KST — integrated N7 actual-conformance expansion `a5bff01` as `d0deb9d`. On dedicated `58091`, the worker passed real stale 412 with current ETag, same-key/same-body idempotent replay, same-key/different-body 409, cursor paging with lossless `900719925474099312345.0001`, and failed batch rollback. Revocation/SSE adoption, admin 401/403, cursor expiry/canceled upload where supported, and same-origin admin browser remain; installed endpoints were excluded.
