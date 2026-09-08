# PocketBase UI source audit — PgBase comparison

The comparison target is the complete `ui/` tree at PocketBase v0.40.3, commit [`5684ee24f1e88f71a2ede73d20cf547fbd509e16`](https://github.com/pocketbase/pocketbase/tree/5684ee24f1e88f71a2ede73d20cf547fbd509e16/ui). The PgBase side is commit `35af64c3e4736af0a8a6d2d3ecb36640e035840a`. This is a product, behavior, and architecture comparison; it is not limited to cybersecurity.

## Coverage and evidence

All 394 PocketBase UI inventory entries were accounted for. The final overlay of `ui-ledger.json` with `ui-css-ledger.json` is 230 directly read handwritten/source files and 164 inspected artifacts. The artifact group contains bundled/vendor outputs, fonts, images, lockfiles, and the generated Remix Icon stylesheet; these were inspected through metadata, provenance, and related source/build references rather than represented as handwritten source. There are no remaining index-only entries after applying the CSS overlay.

Every directly read source file was emitted to the model in bounded independent outputs. File-level evidence is in [`ui-ledger.json`](./ui-ledger.json); the separately completed stylesheet overlay is in [`ui-css-ledger.json`](./ui-css-ledger.json). This avoids treating an earlier oversized combined stream as proof of direct reading.

## What PocketBase's admin UI actually provides

PocketBase ships a Vite/Shablon single-page admin application embedded into the executable, with a `no_ui` build option. Its authenticated surface combines collection and record administration with logs, mail, storage, backups, cron execution, schema import/export, and a SQL console. Public routes cover install, login, password recovery, email confirmation, and OAuth2 redirects.

Collections support base, auth, and view models; field-specific editors and validators; indexes; API rules; view-query dry runs; relation selection; and API examples for JS, Dart, curl, realtime, batch, and auth flows. Record workflows include filter/sort, relation expansion, configurable columns, shift-range bulk selection, bulk delete/export, create/update/duplicate/delete, local draft recovery, file preview/download, auth mail actions, token invalidation, impersonation, and external-provider unlinking.

The operational surface is substantial. Mail settings include SMTP credentials, TLS, AUTH method, EHLO/HELO name, and five test-email templates. Storage can switch between local files and S3-compatible storage. Backup UI covers create, upload, download, delete, restore, scheduled backups, and separate S3 configuration. Cron jobs can be listed and run manually. The SQL console retains query history, confirms mutation-like statements, limits initial rendering, sorts results, and exports CSV. Schema sync can export selected collections, import/merge JSON, preserve or replace IDs, show a field-level diff, and explicitly reconfirm destructive deletion.

The shell also supplies light/dark/auto theme behavior, accent color and branding, responsive navigation, a production control lock, extension hooks, cross-tab collection/settings/theme synchronization, and server validation errors projected onto named fields.

## Where PgBase already compares well

PgBase exposes Collections, Users, Auth collections, Service tokens, Schema operations, and System in its actual route shell ([`ui/admin/src/app.tsx`](../../ui/admin/src/app.tsx#L24), [`ui/admin/src/app.tsx`](../../ui/admin/src/app.tsx#L77)). Its strongest differences follow PostgreSQL's operational model.

- The schema designer offers server preview, permanent-data-loss warnings, compound indexes, view definitions, durable operation receipts, idempotent apply, and recovery of undecided outcomes.
- The record workspace uses server cursors and keeps the cursor chain needed for previous-page navigation ([`record-workspace.ts`](../../ui/admin/src/workspace/record-workspace.ts#L47), [`record-workspace.ts`](../../ui/admin/src/workspace/record-workspace.ts#L106)). Its contract binds update/delete preconditions to record identity and preserves create idempotency ([`ports.ts`](../../ui/admin/src/backend/ports.ts#L169), [`ports.ts`](../../ui/admin/src/backend/ports.ts#L180)).
- PgBase models PostgreSQL-native `bigint`, `numeric`, `timestamptz`, `uuid`, and `jsonb` alongside boolean, integer, text, file, and relation fields ([`ports.ts`](../../ui/admin/src/backend/ports.ts#L30)).
- Record rules have editing and preview flows. Auth realms, registration policy, profile schema, public-user activation, session revocation, and scoped service-token issue/revoke have dedicated screens.
- System status and schema drift preserve partial probe failures as `unavailable`, instead of silently converting them into healthy zero values ([`ports.ts`](../../ui/admin/src/backend/ports.ts#L206), [`ports.ts`](../../ui/admin/src/backend/ports.ts#L225)).
- File contracts include list/upload/delete, short-lived download tokens, and variant downloads ([`ports.ts`](../../ui/admin/src/backend/ports.ts#L277)).

## Material PgBase UI gaps

### Operations console

The largest parity gap is the absence of PocketBase-style logs exploration/export, backup creation/upload/download/restore/scheduling, cron listing/manual execution, mail configuration/test/templates, object-storage configuration/test, SQL console, and schema import/export. PgBase System currently concentrates on health and drift. Administrators must leave the app for routine diagnosis, recovery, and configuration work.

Backup/restore and logs should come first because they close recovery and diagnosis gaps. A SQL console has high power and a larger safety/design cost; it should follow the durable-operation and audit semantics already used elsewhere in PgBase rather than copy PocketBase's keyword-based confirmation check.

### Authentication administration

PgBase auth collections manage realms, registration, and scalar profile schema, but do not expose password identity-field policy, OAuth2 providers, OTP, MFA, token durations/invalidation, redirect allowlists, or auth mail templates. User administration lacks verification/password-reset/email-change sending, impersonation, and external-auth link management.

### Record productivity

PgBase has backend-connected CRUD, filter, sort, relation, and file flows, but lacks configurable persisted columns, shift bulk selection, bulk delete/JSON export, duplication, local draft restore, rich file previews and thumbnail choice, inline relation-record creation, and the auth-record action menu. Collection-local SDK/curl examples are also absent.

### Schema, rule, and shell depth

PgBase explicitly models durable preview/apply/recovery for PostgreSQL DDL. PocketBase is deeper in field-specific configuration, rule autocomplete/context help, relation/rule overviews, schema JSON portability, and visual import diffs. PocketBase also has runtime theme selection, accent/branding settings, an extension script seam, `pbEvent` lifecycle hooks, and cross-tab synchronization. PgBase should keep its receipt and recovery semantics while adding those editor and shell affordances.

## Verified upstream UI defect candidates

These are findings in the pinned PocketBase UI, not features PgBase should copy. [`repro-ui.mjs`](./repro-ui.mjs) extracts the relevant original functions and runs them with narrow stubs. All three assertions pass under Node. These are function-level reproductions, not full-browser tests.

1. [`recordsPickerModal.js`](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/ui/src/records/recordsPickerModal.js) declares `isLoadingPreselected`, while `loadSelected()` writes `isLoadingSelected`. During preselected-record fetching, the combined loading getter remains false.
2. [`cronsList.js`](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/ui/src/settings/crons/cronsList.js) calls undeclared `ApiClient.error(err)` on a cron-run failure. The reproduction gets `ReferenceError` and the per-item `isRunning` flag remains set.
3. [`recordUpsertModal.js`](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/ui/src/records/recordUpsertModal.js#L653-L656) defines `onbeforeopen: () => ... modalSettings.onbeforeopen?.(el)`, but `el` is undeclared. The normal path succeeds when no extension callback is supplied; supplying `modalSettings.onbeforeopen` raises `ReferenceError` before that callback runs.

A separate accessibility typo exists in the rate-limit rule removal button: `araiaDescription` is used instead of `ariaDescription`, so the intended accessible description is not applied.

## Recommended delivery order

1. Add backup/restore and logs using PgBase's durable-operation and partial-failure conventions.
2. Add OAuth2/OTP/MFA/token/mail-template administration, then user verification/reset/impersonation actions with explicit audit events.
3. Add record column preferences, bulk actions/export, duplication, draft recovery, and richer file/relation pickers.
4. Generate collection-local API examples from PgBase's OpenAPI and TypeScript client contracts so examples cannot drift.
5. Add mail, storage, cron, and schema import/export as first-class areas; consider SQL execution only with database-aware statement classification, permissions, timeout, row limits, and durable audit records.
