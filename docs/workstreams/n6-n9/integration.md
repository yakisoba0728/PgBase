# N6–N9 integration

Branch: `codex/n6-n9-integration`

Baseline: installed-status `117a1db217f2054440529d54a80dab7f58b35d6e`, merged product `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`

Plan commits: `133f6448b598d4df706ffbf691f4160a816f385b`, `8dadf42`

Owner: GPT-5.6 Sol integration
Last updated: 2026-09-10 03:44 KST

## Current milestone

G0 and G1 are complete. G2 N6/N7 implementation is active. N6's first stable foundation and all current N7 stable commits are integrated. N6 still owns hooks/jobs/migrations and full external JS application work; N7 still needs browser-driven external Flutter and independent contract review. G3 is a partial integration state, N8/N9 product work is gated, and no final acceptance is claimed.

The branch was created directly from `117a1db`. The two docs-only plans were cherry-picked as `7cedc6e` and `ff3dada`. N6 product handoff must skip worker plan `9a30b2f`; N7 product handoff must skip rebased plan `39b170c`. Only stable product/test/docs commits after those plan commits are eligible.

| Workstream | Worker / branch / worktree | State at 03:38 KST | Integration requirement |
| --- | --- | --- | --- |
| N6 | Chandrasekhar, Sol / `codex/n6-jsvm` / `PgBase-worktrees/n6-n9/n6` | foundation `218f276` integrated as `15676e5`; pinned Goja/CommonJS, fresh VM, exact-number/cancel/quota, options/routes/lifetime and record/file/mail bridge; worker and integrated focused race green | hooks/jobs/migrations, isolated PG/HTTP, external JS app, CLI and complete API handoff; review before close |
| N7 | Einstein, Terra / `codex/n7-dart-sdk` / `PgBase-worktrees/n6-n9/n7` | clean `11923c6`,`ed6fcde`,`52e23ce` integrated as `cae25dc`,`2c97f31`,`85c249a`; file HTTP gate green after fixture fix; integrated analyze and 7 tests green | external Flutter browser, full endpoint/cancel/error/admin/SSE inventory and independent review evidence |
| N8 | Sol worker, assignment through Astra after G3 | not started | detailed N8 design/status first; SQL authorization, parser, transaction/cancel/result limits and UI/browser acceptance |
| N9 | Terra worker, assignment through Astra after G3 | not started | detailed N9 design/status first; signed manifest, atomic replacement, interruption/rollback and native fixture acceptance |
| Integration | GPT-5.6 Sol / this branch | board and plan base ready; N7 404 diagnosed | stable cherry-picks, conflict/API review, cross-feature and final gates, shared docs and final clean commit |

## Ownership and invariants

N6 owns `internal/jsvm`, JS public facade/options/CLI/hooks and required API commons. N7 owns `sdk/dart`, its scripts, examples and N7 docs. Workers do not overwrite each other. The integration lead owns common composition, route/OpenAPI/generated files, `go.mod`/`go.sum` resolution and all shared status documents: `TODO.md`, `docs/next-work.md`, `docs/project-status.md`, `docs/pocketbase-current-reassessment.md`, and this file.

System migrations 1–40 are immutable. N6/N7 currently require no new system migration. If N8 or N9 proves a durable schema requirement, this document allocates 41 or later before code is written. Product Go files retain the 800-line limit; final global budget movement requires measured feature delta and independent rationale.

No N6–N9 test may target the installed PostgreSQL 17.9 on `55440`, MinIO on `59000`, HTTP on `8090`, its binary, Docker resources or userdata. Completed N1–N5 install/rollback validation is historical evidence and will not be rerun.

## Isolated service allocation

| Owner | Docker project / resources | Loopback ports | State |
| --- | --- | --- | --- |
| N7 | `pgbase-n7`; PostgreSQL and PgBase containers; task-owned local file directory/volume | PostgreSQL `55441`, HTTP `58091` | active; file storage config narrow fix requested |
| N6 | worker-assigned names only | unallocated until worker reports exact values | no container observed at 03:35 KST |
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
