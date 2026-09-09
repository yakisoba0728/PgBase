# JavaScript extensions

PgBase can load an operator-reviewed JavaScript bundle and turn its exports into HTTP routes, lifecycle hooks, durable jobs, and user migrations. The runtime is opt-in. It uses the same record, file, mail, hook, job, and migration services as native Go code, so JavaScript does not bypass collection rules, ETags, idempotency, audit events, storage policy, or the user-migration ledger.

The implementation pins `github.com/dop251/goja` at `v0.0.0-20260906210903-70ad66ec7ce4` and `github.com/dop251/goja_nodejs` at `v0.0.0-20260212111938-1f56ff5bcf14`. Goja is an in-process engine for trusted local code. It is not an operating-system sandbox for untrusted tenants and does not provide a per-VM hard heap limit.

## Enable a bundle

Set an absolute source directory and the capabilities the operator permits:

```sh
export PGBASE_JS_DIR=/srv/pgbase/extensions
export PGBASE_JS_CAPABILITIES=records,files,mail,log
export PGBASE_JS_JOB_SERVICE_SUBJECTS='{"daily-report":"00000000-0000-4000-8000-000000000001"}'

pgbase js check
pgbase js types ./pgbase.d.ts
pgbase serve
```

`PGBASE_JS_JOB_SERVICE_SUBJECTS` maps each manifest job to an existing service-token subject. A script cannot select a user, administrator, or service identity. PgBase refuses startup if a requested capability is absent, a job has no service subject, or `files`/`mail` is requested without its backing service.

Embedding applications set `Options.JavaScript` to `*JavaScriptOptions`. `ValidateJavaScript` checks and compiles the immutable source snapshot without opening the application database or invoking host effects. `App.JavaScriptMigrations` returns migrations for composition with Go user migrations. `App.ExtensionsForRequest` exposes the same admitted-request record, mail, and file facade to Go handlers; it rejects contexts that did not pass through PgBase route admission and expires with the original request.

## Manifest and modules

The source root contains `pgbase.scripts.json`:

```json
{
  "format": "pgbase.scripts/v1",
  "routes": [{
    "method": "POST",
    "pattern": "/extensions/tasks",
    "access": "user",
    "handler": { "module": "routes.js", "export": "createTask" },
    "capabilities": ["records"],
    "maximumRequestBytes": 1048576,
    "maximumResponseBytes": 1048576
  }],
  "hooks": [{
    "event": "record.create",
    "priority": 20,
    "after": { "module": "hooks.js", "export": "afterCreate" },
    "capabilities": ["log"]
  }],
  "jobs": [{
    "name": "daily-report",
    "schedule": "0 2 * * *",
    "timezone": "UTC",
    "handler": { "module": "jobs.js", "export": "run" },
    "capabilities": ["records", "mail"]
  }],
  "migrations": [{
    "id": "20260910090000_receipts",
    "module": "migrations/receipts.js",
    "up": "up",
    "down": "down",
    "dependencies": ["migrations/shared.json"]
  }]
}
```

Handlers are synchronous CommonJS exports. A module may `require("pgbase")` or a relative `.js`/`.json` file from the immutable bundle. There is no npm/package lookup, filesystem fallback, remote import, ESM loader, `process`, Node core module, browser DOM, network client, timer, event loop, or Promise support. Relative imports, JSON modules, and CommonJS cycles are supported. Source maps do not fetch files or URLs.

The loader rejects absolute/traversing paths, symlinks, special files, missing exports, unknown manifest fields, and route or migration ID conflicts. Each source file is at most 1 MiB; a bundle is at most 8 MiB and 128 source files. The deterministic digest covers the manifest and every loaded source byte. Runtime values and module globals are fresh for each root invocation.

Migrations have a stricter closure. Their checksum source contains the entry module plus every manifest-declared dependency. Every migration `require` must be a direct call with one static string; aliases, dynamic paths, function-passed `require`, property access, and undeclared Down-only dependencies are rejected before database access. This keeps historical source executable and checksum-complete.

## Native module

Generate the exact TypeScript declaration with `pgbase js types`. The runtime exports these synchronous namespaces from `require("pgbase")`:

| Namespace | Operations | Contract |
| --- | --- | --- |
| `records` | `list`, `get`, `create`, `update`, `delete`, `batch` | Uses the admitted principal, collection rules, validators, N5 request/record hooks, ETag preconditions, idempotency replay, audit/outbox, filters, sorting, expansion, and cursor pagination. Raw SQL and identity selection are unavailable. |
| `files` | `list`, `upload`, `delete`, `token`, `read`, `open`, `readChunk`, `close` | Uses the configured file driver and record/file authorization. `open` returns an invocation-local opaque handle. Only one stream may be active; chunks are at most 64 KiB and one invocation can read at most 3 MiB. Close handles explicitly; PgBase also closes them on success, throw, cancellation, or host failure. `read` and `upload` fail with `file_stream_active` while a stream is open. |
| `mail` | `send` | Uses configured SMTP and `mail.send` lifecycle hooks. External delivery can be duplicated across a process crash; use an application outbox/idempotent consumer when that matters. |
| `log` | `debug`, `info`, `warn`, `error` | Accepts one bounded message and up to 16 bounded string fields. PgBase adds request context. Do not log tokens or user payloads. |
| `migration` | `exec`, `query`, `applySnapshot` | Exists only inside a user-migration callback and uses its transaction. SQL arguments are parameterized values. The handle cannot commit, rollback, escape the callback, or access the normal request runtime. |
| `exact` | `exact(value)` | Preserves a JSON number lexeme that cannot be represented safely by a JavaScript `number`, including large integers and decimals. |

Host arguments and results use a strict JSON codec. `undefined`, cycles, non-finite numbers, unsupported objects, and integers outside JavaScript's safe range are rejected. Omit optional properties instead of assigning `undefined`. Use `pgbase.exact("9007199254740993.125")` for exact output and keep exact input values as the opaque exact-number object supplied by PgBase.

## Routes and authority

Route access is `public`, `user`, `admin`, or `service` and follows the native custom-route admission contract. The request contains method, path, path values, query values, a small allowlist of non-secret headers, an admitted principal when present, and either JSON or a text body. Authorization, cookies, bearer tokens, passwords, OTPs, and raw session material are never passed to JavaScript.

A route returns `{status, headers?, json?}` or `{status, headers?, body?}`. Response headers are limited to `Cache-Control`, `Content-Disposition`, `Content-Language`, `Content-Type`, `ETag`, `Location`, `Retry-After`, and `X-Extension`. Route registration shares native collision checks and the global custom-route count. A public route remains anonymous even if a caller sends credentials.

The operator capability list is a ceiling and each descriptor requests a subset. The effective authority is the intersection of that capability and the admitted principal. Record and file calls cannot construct a different principal. Migration SQL is deliberately isolated from request, hook, and job code.

## Lifecycle hooks

A hook receives `{event, phase, data, failed, state?}`. Before may return a documented `patch` and bounded `state`; After receives detached data and observes the outcome. Priority and Go/JavaScript ordering use the shared lifecycle registry. Record transaction retries may run Before again, while After is commit-only best effort. Hook exceptions do not create durable retry semantics.

The accepted event names are:

- Request and records: `request`, `record.before`, `record.after`, `record.list`, `record.view`, `record.create`, `record.update`, `record.delete`, `record.batch`.
- Mail and files: `mail.send`, `mail.otp`, `mail.verification`, `mail.password_reset`, `mail.email_change`, `mail.smtp_test`, `file.upload`, `file.delete`, `file.token`, `file.download`, `file.transform`.
- Schema and application: `schema.import.preview`, `schema.import.apply`, `collection.create`, `collection.update`, `collection.delete`, `app.boot`, `app.serve`, `app.terminate`.
- Authentication: `auth.password`, `auth.oauth.begin`, `auth.oauth.complete`, `auth.otp.request`, `auth.otp.complete`, `auth.verification.request`, `auth.verification.confirm`, `auth.password_reset.request`, `auth.password_reset.confirm`, `auth.email_change.request`, `auth.email_change.confirm`, `auth.impersonate`.
- Realtime, storage, and backup: `realtime.connect`, `realtime.subscribe`, `realtime.message`, `storage.settings.list`, `storage.settings.update`, `storage.settings.test`, `backup.create`, `backup.restore`, `backup.delete`.

Only record request input, mail message fields, request headers, batch operation inputs, and schema-import preview documents are mutable in their corresponding Before phase. Apply/checksum/idempotency/actor/file/auth/storage/backup/realtime data is read-only. Unknown Go payload types are rejected instead of being reflected into JavaScript. Storage object keys and credential/error fields are redacted. See [lifecycle parity](lifecycle-parity.md) for each event's transaction, veto, retry, and After timing.

## Jobs and migrations

JavaScript jobs use the durable PgBase scheduler. Interval and five-field cron schedules, IANA time zones, persisted administrator edits, leases, retry state, and worker fencing stay in the existing job tables. The handler receives `jobId`, `name`, exact `fenceGeneration`, `runKey`, `scheduledAt`, and `occurrenceKey`. `occurrenceKey` identifies one logical scheduled occurrence, stays stable across its retries, and changes for the next schedule; use it for one idempotent effect per occurrence. `runKey` identifies one fenced claim attempt and changes on retry.

JavaScript and Go user migrations are merged and sorted by migration ID. Duplicate IDs fail. Up and supported Down run in the existing transaction, advisory lock, source/history ledger, schema validation, and cache-refresh path. `applySnapshot` preserves the N1 deterministic snapshot/diff rules for rename, relation, views, indexes, and field options. An irreversible migration must declare a reason instead of a fake Down. Cancellation and failure roll back DDL and ledger changes. `history-sync` keeps its existing safe source/physical-schema checks and never deletes history merely because local source is absent.

Use the same frozen bundle for a migration command:

```sh
pgbase migration list --js-dir /srv/pgbase/extensions
pgbase migration up --js-dir /srv/pgbase/extensions
pgbase migration history --js-dir /srv/pgbase/extensions
pgbase migration down --js-dir /srv/pgbase/extensions
```

Development reload never runs migrations automatically.

## Limits, cancellation, and errors

The defaults are eight concurrent root invocations, 32 queued calls, 250 ms admission wait, five-second execution timeout, 30-second configurable timeout ceiling, 256 host calls per execution chain, 1 MiB input, 4 MiB output, JSON depth 64, and JavaScript call stack 256. A nested host-to-hook invocation shares its root slot, deadline, and cumulative host-call budget, with maximum depth eight.

PgBase interrupts running JavaScript when the caller, timeout, reload, or application shutdown cancels it. Context reaches PostgreSQL, SMTP, and file operations. `App.Close` rejects new calls, cancels queued and active invocations, closes open file readers, and waits for drain. Goja cannot interrupt an arbitrary blocking native Go function, so the native surface contains only context-aware PgBase operations.

JavaScript may observe host codes such as `permission_denied`, `not_found`, `validation_failed`, `etag_mismatch`, `file_too_large`, `file_handle_limit`, `file_stream_active`, and `operation_failed`. Runtime classes include `js_busy`, `js_timeout`, `js_canceled`, `js_limit_exceeded`, `js_capability_denied`, `js_invalid_result`, and `js_execution_failed`. HTTP routes expose 503 for busy/closed, 504 for timeout, 403 for capability denial, and a sanitized 500 for other uncaught failures; raw JavaScript stacks and internal service errors stay in bounded server diagnostics.

CPU loops are interruptible. Heap exhaustion can still terminate the process because Goja has no hard per-runtime heap quota. Run applications that need a hard memory boundary under an operating-system or container memory limit and rely on process restart. Keep scripts operator-reviewed, avoid attacker-controlled code generation, and bound data before constructing large arrays or strings.

## Development reload

`pgbase js serve --watch [--dir DIR] [--poll DURATION]` is a development supervisor. It snapshots and validates the complete candidate bundle before replacing the running application. Invalid syntax or configuration leaves the current generation running. A valid change drains the old application and starts the new snapshot; if startup fails, the last validated generation is restored. Only one listener and job generation runs at a time.

The supervisor performs sequential application replacement in one process and can have a short listener interruption. It does not provide production hot reload, managed-backup mode, migration execution, or a per-generation OS memory boundary. Production deployments should validate with `js check`, run migrations explicitly, and restart through the normal service manager.

The complete reference application is in [examples/javascript](../examples/javascript/README.md).
