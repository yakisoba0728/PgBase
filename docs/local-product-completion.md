# Local product completion after the PocketBase source audit

This increment implements the five local product areas approved after the source comparison in `pocketbase-source-audit-2026-09-09/assessment.md`. The baseline is main `35af64c` (PR #28). It does not claim PocketBase wire compatibility or complete PocketBase feature parity. External staging, JSVM, Dart and arbitrary SQL remain outside this increment.

## Field contracts and schema changes

Field inputs accept `id` when updating an existing field. A rename keeps the ID and physical column; the public field name changes. A new field omits its ID. Existing clients can still identify unchanged fields by exact name. Renaming to an occupied name, duplicate field identities and physical-column collisions are rejected. Read the catalog again after apply before starting a new edit.

`options` supports:

| Type | Options |
| --- | --- |
| text | `minLength`, `maxLength`, Go regular-expression `pattern`, `format` (`email` or `url`), `allowedDomains`, `allowedValues` |
| integer / bigint / numeric | `min`, `max` as plain decimal strings |
| jsonb multi-select | `allowedValues` as strings, `minSelect`, `maxSelect`; values are arrays of distinct choices |

Lengths count Unicode code points. Allowed domains include their subdomains. Use `^`/`$` for full-pattern matches. Empty `allowedValues` means unrestricted; selection counts require a nonempty choices list. Required preserves PgBase's SQL `NOT NULL` behavior: false, zero and empty text are allowed unless another constraint rejects them. JSON literal null is distinct from SQL NULL. Relations retain their own selection-minimum contract.

Omitting `options` on an update preserves existing constraints. Send `options: {}` to clear them. The admin editor preserves field IDs and sends explicit cleared options. New or tightened constraints are checked against existing rows before the schema transaction commits; an invalid existing value aborts the change. Direct external SQL writers must follow the same contract: these application validators are not a substitute for PostgreSQL constraints on arbitrary external writes.

Migrations 22 and 23 are additive; migrations 1–21 remain immutable. For the offline v21 → v23 upgrade, stop all old instances and back up the whole database and local files, then run the new binary with `PGBASE_AUTO_MIGRATE=false pgbase migrate up`. Require `current=23 latest=23 dirty=false` and `pgbase doctor --json` reporting `ok: true` before starting it. The bulk command is intentional: `--through 23` only accepts current v22/v23, and this binary refuses `--through 22` because 22 is not its latest embedded target. A version-21 catalog remains readable for migration preflight.

## Records and authentication administration

Record lists support selection, per-record ETag deletion with partial-failure results, bounded cursor-based JSON export and visible-column preferences. Export walks at most 100 pages and reports an incomplete export rather than presenting a partial result as complete. Duplication starts a fresh create attempt and excludes system and file fields. Local drafts are scoped by origin, administrator, collection and record; common password/token/secret field names are excluded. Storage availability is optional; logout and saving must remain usable when browser storage is denied. A record field with sensitive content under an ordinary name is still application data, so avoid using persistent drafts for such content.

Auth collections expose registration/profile configuration, password and MFA policy, OAuth provider configuration, SMTP and mail templates. Provider and SMTP secrets are encrypted in PostgreSQL using the existing application secret machinery, never returned in settings reads, and preserved when an edit leaves the secret blank. Keep the token secret with the separately protected project configuration. Configured providers and mail delivery load realm settings at runtime; a process-wide SMTP configuration is a fallback. Configure the public URL for mail action links.

Policy changes can revoke existing sessions. Disabling MFA removes that realm's TOTP factors and recovery codes; re-enabling requires enrollment again. The administrator UI offers user verification state, verification mail and password-reset mail actions. Local HTTP OAuth and insecure SMTP are restricted to explicit loopback development configuration.

## Operations

System includes an operations console. The request log retains the latest 1,000 completed requests in memory per instance and returns pages of 50, with status-class filtering. It stores route templates, method, status, time and duration; it does not store headers, raw paths, query values or request bodies. Restarting clears this diagnostic window. It is not a durable audit-log archive.

Scheduled jobs use existing registered Go handlers. The console lists execution state, edits enabled/interval settings and queues a run through the existing worker. Intervals remain `@every` 1 second through 24 hours. Running leases cannot be stolen by the console. Administrator settings survive process restarts; explicit embedder `Schedule` calls can still replace defaults. A binary without jobs enabled reports that fact.

Local backups require the writer gate and an offline server; restore uses a separate empty database and a new file directory. See `local-backup-console.md` for the final executable workflow and its validation results.

## API examples and SDK

Each record list includes examples selected for user, anonymous or service identity, plus the actual field constraints. View examples are read-only. Generated user CRUD examples are typechecked against the shipped SDK declarations. Examples use placeholders and do not copy administrator credentials into source code.

The TypeScript SDK adds `PgBaseAdminClient` for browser administrator sessions, catalog, schema preview/apply/status, logs and jobs. It keeps CSRF tokens in memory and uses browser cookies; it does not attach a public-user Bearer token to administrator requests. `ApprovedSchemaPlan` snapshots the preview body before awaiting the server, preserves the operation and idempotency IDs across apply retries and exposes a copy for inspection. Field IDs/options are typed. The existing `PgBaseClient` remains the application user client.

## Measured structural limits

This is explicitly approved new product scope. The staged report against `35af64c` measures 43,538 Go runtime lines and 4,956 administrator UI lines (48,494 combined), with gross runtime additions/deletions of +3,022/−171. The comparison category excludes eight migration embedding lines included by the mechanical runtime counter. The data group is 19,382 lines. Production Go has 37 interfaces, 698 exported declarations and 37 constructors; UI has 76 direct page state/effect calls and 169 exports. The separately measured TypeScript SDK has 740 source lines. These values establish zero-slack ceilings for this approved scope. Reproduce with `GOTOOLCHAIN=go1.26.8 go run ./tools/repostats --compare 35af64c --pretty` after staging new files. The zero-file-over-800-lines rule stays in force. The HTTP route ceiling increases from 99 to 113 for ten auth administration routes and four operations routes; method-agnostic routes stay at three. With the existing histogram and status buckets, the conservative HTTP series ceiling is `(260 × 6 × 17) + 260 = 26,780` per instance. The field-capability probe adds one fixed doctor query (35 → 36), independent of catalog size.

## Validation

- Go 1.26.8 full unit suite with the race detector and skip policy passed.
- The full PostgreSQL 18 integration suite with the race detector and skip policy passed, including the measured structural budgets. PR CI also runs the complete PostgreSQL 17 suite.
- Pinned Go lint: zero issues; integration-build vet passed.
- Admin typecheck, 34 Vitest files / 450 tests, and all 8 authenticated Chromium workflows passed. Browser coverage includes preserved drafts, actual export JSON, rename with retained data, allowed-value rejection, authentication settings, and a 390px viewport.
- SDK tests, generated example compilation, live PostgreSQL/HTTP SDK conformance, and local npm packaging passed.
- Six local binaries built: Linux, macOS and Windows on amd64/arm64.
- A protected backup was retained before upgrading the existing local project from v21 to v23; readiness is healthy and doctor reports no findings. Restoring the pre-upgrade backup into a separate temporary database also passed the original binary’s doctor check.

The full source comparison remains in `pocketbase-source-audit-2026-09-09/`. Passing this increment means the approved local workflows are implemented and verified; it does not turn the remaining PocketBase differences into completed features.
