# Administrator UI rebuild

> Historical record of the previous administrator UI, which was replaced by the from-scratch rewrite on branch `frontend-rebuild` (2026-11-13; see [project status](project-status.md) §11).

Work started 2026-11-12 from `82411a18fa6a42a225b0609c9e3dd176b0916c14`.

## Product direction

[PocketBase](https://pocketbase.io/) is the reference for navigation, density and administrator workflows:
a compact primary navigation bar, searchable collection navigation, compact
record tables, focused collection editors and grouped settings. PgBase retains
its PostgreSQL schema planning, operation receipts, exact numbers, sessions,
record revisions and access-control contracts. Visual similarity does not imply
PocketBase wire or SDK compatibility.

## Implementation scope

- Shared light/dark design system, responsive shell and contextual navigation.
- Collection browsing and schema, field, index and access-rule editing.
- Record browsing, filtering, sorting, selection, creation and editing.
- Auth collection policy, account and administrator workflows.
- Logs, backups, jobs, realtime, SQL and schema-operation workflows.
- Application, mail, runtime policy, storage and transfer settings.
- Setup, login, password recovery and OTP interaction.

## Acceptance

- Existing supported routes and operations remain reachable.
- Hidden tabs preserve drafts; pending writes cannot be submitted twice.
- Destructive operations retain confirmation and server preview requirements.
- Capability checks, cancellation, session expiry and error recovery remain valid.
- Keyboard navigation, focus visibility, narrow screens and both themes work.
- UI unit/type/build checks and embedded asset reproduction pass.
- Applicable Go/SDK regression checks and isolated browser workflows pass.
- Structural measurements describe the resulting implementation without hiding files.

No existing installation, user database or deployment is changed by this work.
Integration results are recorded below.

## Integrated verification

The implementation is on `codex/pocketbase-admin-rebuild`. Server API contracts
and schema version 65 are preserved. All existing administrator surfaces
remain available, with a new `/admin/logs` entry point.

- Go 1.26.11: complete unit suite with race detector and skip-policy guard;
  `go vet ./...` passed.
- PostgreSQL 17.11 integration/race: complete affected packages passed:
  relation 1.997s, query 128.818s, recordops 170.438s. The first recordops run
  was interrupted by task-fixture teardown; its complete rerun passed after
  recreating the fixture. No test retry or server workaround was added.
- Node 24.21.0: 57 UI test files / 719 tests passed, typecheck and production
  build passed. The final three embedded assets reproduced byte-for-byte.
- TypeScript SDK: build, strict consumer types, conformance script and 39 Node
  tests passed.
- Runtime source measurement: 107,943 lines (Go 94,993; UI 12,950),
  +979/-714/net +265 against the starting commit. UI page hooks 233; exports
  unchanged at 355. No runtime file exceeds 800 lines. The measured ceilings
  are updated without classifier exclusions or spare growth allowance.

Regression fixes include stale resumed sessions overwriting fresh credentials,
ambiguous initial restore unlocking setup, stale import receipts clearing newer
draft protection, hidden record selections surviving filter changes, compound
index comma input, export overwriting an import draft, partial operational
read failures and stale log details, plus administrator password draft loss.

Real browser acceptance also exposed a shared relation-binding defect: a valid
logical field rename retaining its physical column caused unrelated record
deletions to fail with `schema_conflict`. Bindings now validate physical columns
separately, preserve duplicate-column rejection and resolve relation storage
using physical names. Scalar rename own/unrelated deletion and single/multiple
relation rename read/expand/update/delete have PostgreSQL regressions. The
original failing browser workflow passes on the same retained database after
the fix, without a retry or delay.

Browser acceptance uses disposable PostgreSQL 17.11 databases and separate
HTTP servers; it never uses the existing installation or user database.

- Final fresh authenticated suite: 29 passed, four existing opt-in cases skipped,
  50.5 seconds, no failures or retries. Onboarding: one passed. The 30 distinct
  passing scenarios include native validation across hidden tabs, actual CRUD,
  exact-number import/export, auth administration, records and relation workflows,
  schema recovery, operational redaction, both themes, 1440/1024px navigation
  hit-testing and 390px overflow/focus checks.
- The four opt-ins require managed restore/backup hosts and/or MinIO. Real
  external provider delivery, cloud S3, production deployment and the Dart SDK
  are not revalidated by this change.
- Browser driver: Node 26.13.0 and Chromium 153; UI unit/build validation uses
  the pinned Node 24.21.0. Backend and Go regression checks use Go 1.26.11.
- Visual evidence and browser logs are in the ignored local directory
  `.local/ui-rebuild-evidence/`; the task-owned fixture is stopped after testing.
