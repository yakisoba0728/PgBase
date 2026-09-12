# N6 independent Standards/Spec review

Reviewed `2939f1543e4889c0ffa967eae398c91d0ecbb6ce...94b32b3fe1b5bf1f066d3b9a25e818ea15ecf279` in `codex/n6-review` on 2026-09-10. The code scope is the N6 JSVM foundation (`218f276` / `15676e5`) and domain-service, hook, and migration binding (`712900b` / `94b32b3`); Dart/N7 changes are excluded.

Spec sources: `docs/superpowers/plans/2026-09-10-n6-n9-execution.md`, `docs/workstreams/n6-n9/N6.md`, and the public custom-route/extension contract. The review treats durable jobs, CLI/watch, extended migration operations, external app acceptance, browser/SDK, and stress work as later deltas recorded in N6, not defects in this bounded slice.

## Standards

No documented repository-standard violation or actionable baseline smell was found in the N6-only code diff. `git diff --check` is clean. The foundation keeps the JSVM boundary narrow: a snapshot loader, fresh Goja runtime, typed host bridge, and existing domain services rather than a parallel router, record writer, or migration ledger.

## Spec

### P1 — admitted extension authority can outlive or detach from its request

`ExtensionsForRequest` checks admission only when the public facade is obtained, then stores the principal and request ID in `extensionHost` ([`pgbase/extensions.go`](../../../pgbase/extensions.go) lines 26–37). Every facade method subsequently accepts an arbitrary caller context and `extensionCall` serializes it to the host without verifying that it still carries the same admitted request marker ([`pgbase/extensions.go`](../../../pgbase/extensions.go) lines 83–132 and 253–272). A custom Go route can therefore obtain an `ExtensionContext` during an authenticated request, retain it, and later invoke it with `context.Background()` after disconnect/cancellation. The operation still runs as the original user because `extensionHost` uses the captured subject and request ID ([`pgbase/extension_bridge.go`](../../../pgbase/extension_bridge.go) lines 33–48 and 115–198).

This violates the N6 contract that `ExtensionContext` holds admitted identity **and context**, that a plain context must not become an admitted request, and that authority/cancellation flow through the existing domain boundary ([`N6.md`](N6.md) lines 67–72 and 115–116). Preserve an unforgeable admission capability on the facade and require each operation context to carry the matching request ID and subject, or derive calls from the stored admitted context while retaining the caller's tighter deadline. Add a unit/integration regression that captures a valid facade, cancels its request, then proves a `context.Background()` call is rejected and performs no record operation.

### P1 — frozen migration source does not validate the declared dependency closure

`MigrationBundle` copies only the entry module plus manifest-declared `dependencies`, but `validateReferences` only validates those names; it does not resolve their CommonJS import closure ([`internal/jsvm/bundle.go`](../../../internal/jsvm/bundle.go) lines 100–149 and 270–310). Startup validation compiles the complete application bundle ([`internal/jsvm/runtime.go`](../../../internal/jsvm/runtime.go) lines 372–412), so a migration module can import an undeclared app file and pass `New`/`ValidateJavaScript`. Its isolated migration bundle then omits that import and fails only when `Up` or `Down` reaches it.

The review reproduced this with `migration.js -> declared.js -> undeclared.js`, declaring only `declared.js`: `LoadBundle` and `MigrationBundle` both succeeded (`go test ./internal/jsvm -run '^TestReviewMigrationBundleDoesNotRejectUndeclaredNestedDependency$' -count=1 -v`, exit 0); the review fixture was removed afterward. N6 requires every local migration import to belong to the manifest-declared migration-only dependency set and treats source/path errors as startup/check errors before database effects ([`N6.md`](N6.md) lines 156–158 and 183). Construct the migration subset first and validate/compile every migration handler against that subset during JavaScript validation. Reject any static or invoked dynamic local require that resolves outside the declared set, and cover a nested undeclared require plus an undeclared import reached only from `Down`.

## Verification

- `git diff --check 2939f15...94b32b3 -- internal/jsvm pgbase internal/api/custom_routes.go go.mod go.sum` — exit 0.
- Local review-only migration-closure reproduction above — exit 0; fixture removed.
- No live service, Docker, UserData, port `8090`, `55440`, `59000`, or the owner/lead-coordinated N6 PostgreSQL fixture was used.

## Delta review — jobs, CLI, watch, and declarations

Reviewed `94b32b3fe1b5bf1f066d3b9a25e818ea15ecf279...41852b5c803891de004f8e810c4ff14699f2eaa5`. This delta covers the JS CLI/env surface, migration CLI composition, source snapshots/watch reload, durable job wiring, and the declaration file. N7 changes and the remaining hook/migration/external-app work are excluded. The two earlier P1s are unchanged by this delta and await the owner’s narrow fixes.

### Standards

No documented repository-standard violation or actionable baseline smell was found in the delta. The new code keeps CLI parsing, environment decoding, snapshotting, and job execution at their existing seams. `git diff --check` is clean.

### Spec

#### P1 — migration `--js-dir` validates after opening the database

`runUserMigration` parses `--js-dir`, then calls `openDatabase` before it applies that directory to `options.JavaScript` and constructs the application ([`internal/cli/user_migration.go`](../../../internal/cli/user_migration.go) lines 72–96). An invalid JS manifest, path, export, or capability passed through `--js-dir` therefore reaches the database before `pgbase.New` validates the bundle.

N6 requires source/path/capability failures to be configuration errors rejected before the database connection, route registration, or worker start ([`N6.md`](N6.md) line 183); its CLI contract also says the same manifest source is used by migration subcommands ([`N6.md`](N6.md) lines 156–160). Preload and validate the effective JavaScript options immediately after parsing the flag, before `openDatabase`; pass the validated immutable bundle/options into the subsequent app construction. Add a DB-free regression whose bad `--js-dir` returns exit 2 even when `PGBASE_DATABASE_URL` is unreachable.

#### P1 — a broken stdout leaves the initial watch generation running

After `startWatchedApplication` starts `app.Run` in a goroutine, `superviseJavaScript` writes the initial generation status. If that write fails, it returns directly without closing the just-started app or awaiting `result` ([`internal/cli/javascript_watch.go`](../../../internal/cli/javascript_watch.go) lines 63–69 and 144–151). A closed stdout pipe can therefore make `pgbase js serve --watch` return while its listener, jobs, and runtime stay alive until some later cancellation.

This contradicts the N6 shutdown contract, which requires the supervisor to retain one listener/job owner and to clean the VM/reader lifecycle ([`N6.md`](N6.md) lines 175–177 and 205). On the status-write error path, close `app` with the configured shutdown timeout and wait for `result` before returning the joined error. Cover it with a failing `IO.stdout` writer and assert that the listener becomes unavailable and no job generation remains.

### Review-only confirmation

`js check` currently accepts a job manifest without the mandatory `JobServiceSubjects` entry: `go test ./internal/cli -run '^TestReviewJavaScriptCheckAcceptsJobWithoutServiceSubject$' -count=1 -v` exited 0. This is not filed as a defect because the current `js check` contract can reasonably be read as bundle validation rather than full runnable-app validation; the fixture was removed. If the CLI is intended to certify runnable configuration, make it reject that case before reporting the bundle valid.

## Re-review — `ceacdf087562b54f76f191b7d9c5367d874ba4ad`

Reviewed the four reported P1 remediation paths and the new hook DTO/patch surface. The prior retained-facade, direct/nested and `Down`-only dependency, migration preflight, and initial-watch-status-write cases are addressed by this commit: request authority is now cancellation-bound, direct static requires are checked against the declared set, JS configuration is validated before `openDatabase`, and the initial watch error closes then joins `Run`.

### Standards

No documented repository-standard finding. `git diff --check ceacdf0^ ceacdf0` is clean. The new code stays at the existing loader, bridge, CLI, and hook seams. The hook type switch accepts the supported N5 payload kinds and rejects unknown Go values; the DTO codec preserves JSON numbers, copies patch inputs, redacts the present storage/credential/error fields, and keeps batch/schema mutation boundaries narrow.

### Spec

#### P1 — aliased CommonJS `require` bypasses frozen migration dependency closure

[`internal/jsvm/bundle.go:382`](../../../internal/jsvm/bundle.go:382) recognizes a require only when the call callee is the identifier literally named `require`; [`internal/jsvm/bundle.go:357`](../../../internal/jsvm/bundle.go:357) otherwise has no binding/escape analysis. A migration can therefore write `const load = require; exports.down = () => load("./hidden").run();`. `LoadBundle` accepts it, and [`internal/jsvm/bundle.go:122`](../../../internal/jsvm/bundle.go:122) freezes only the entry plus explicitly declared dependencies, leaving `hidden.js` out. The migration consequently still defers its source error to `Down` execution.

This was reproduced in a review-local temporary unit fixture: `go test ./internal/jsvm -run '^TestReviewMigrationAliasRequireBypassesDeclaredClosure$' -count=1 -v` passed because both `LoadBundle` and `MigrationBundle` accepted the alias while the subset lacked `hidden.js`; the fixture was removed immediately afterward. Migration validation must either resolve aliases/indirect calls to `require` or reject any non-direct escape/use of `require`, so every executable import remains within the declared frozen set.

### Verification

- `go test -race ./internal/jsvm -run '^(TestMigrationBundleFreezesOnlyDeclaredSources|TestLoadBundleRejectsUndeclaredMigrationDependencyClosure|TestLoadBundleRejectsDynamicRequireInMigrationSource)$' -count=1` — pass.
- `go test -race ./internal/cli -run '^TestJavaScriptMigrationPreflightsBundleBeforeDatabase$' -count=1` — pass.
- `go test -race ./pgbase -run '^TestJavaScriptHook' -count=1` — pass.
- The review did not run PostgreSQL integration tests, a live listener, Docker, UserData, or the coordinated N6 fixture.

## Follow-up re-review — `007e5242a14110000042c92d0ead712b923eb536`

The aliased-`require` P1 is resolved. [`internal/jsvm/bundle.go:383`](../../../internal/jsvm/bundle.go:383) counts each `require` identifier and [`internal/jsvm/bundle.go:428`](../../../internal/jsvm/bundle.go:428) rejects every occurrence that is not a direct, single static-string call. The added unit covers `const load = require`; direct, nested, `Down`-only, and dynamic cases remain covered. A review-local fixture also confirmed that an ordinary object property named `require` is not rejected; it was removed afterward.

No Standards or Spec finding remains in this narrow fix. `git diff --check 007e524^ 007e524` is clean, and `go test -race ./internal/jsvm -run '^(TestMigrationBundleFreezesOnlyDeclaredSources|TestLoadBundleRejectsUndeclaredMigrationDependencyClosure|TestLoadBundleRejectsDynamicRequireInMigrationSource|TestLoadBundleRejectsAliasedRequireInMigrationSource)$' -count=1` passed. PostgreSQL acceptance was not run by this review because it requires the coordinated fixture.

## Integrated re-review — `21b829367c1f61aab54123761490e12299ae7b5f`

The integration copy preserves the alias-closure fix unchanged. Re-running the direct alias, dynamic, nested, `Down`-only, and frozen-source race unit set passed. The eight-line identifier/call-count rule rejects an escaped `require` while keeping ordinary object properties named `require` outside its scope; no bypass or contract widening was reproduced.

The added integration acceptance test matches the bounded N6 migration contract: it exercises snapshot preservation across a rename, relation, view, index, field options and existing data; mixed Go/JS ordering and duplicate IDs; history-sync without a schema-version change; reverse snapshot application; cancellation rollback; and concurrent runner fencing. Static review found no Standards or Spec issue. Its PostgreSQL execution remains unrun here because the test requires the owner/lead-coordinated fixture.

## Final owner delta review — `f675074`, `6f2f605`, `e440b13`

### P1 — JavaScript scheduled jobs cannot distinguish a retry from the next scheduled occurrence

[`pgbase/javascript_jobs.go:13`](../../../pgbase/javascript_jobs.go:13) exposes only a persistent `jobId`, name, changing fence generation, and attempt `runKey`. The scheduler increments the fence on every claim of the same scheduled-jobs row ([`internal/jobs/service.go:189`](../../../internal/jobs/service.go:189)); neither `JobRun` nor the JS payload carries a durable occurrence ID. The external reference job then uses its fixed `jobId` as the logical idempotency key ([`examples/javascript/extension/job.js:10`](../../../examples/javascript/extension/job.js:10)).

After its first claim commits and intentionally retries, every later `@every 1h` claim creates the same record key and receives a replay. It reports success but performs no new delivery. This contradicts the documented distinction that `runKey` is one attempt and a stable logical task/event ID must deduplicate only retries ([`docs/javascript.md:109`](../../../docs/javascript.md:109)). Persist and expose a logical scheduled-occurrence identifier that survives retries but changes for the next schedule, then make the example use it; a generated task/outbox record with that identifier is also acceptable.

The storage publication fix is otherwise sound on static review: idle cleanup avoids queueing an exclusive lock and publication uses a nonblocking shared lock to return `ErrStorageConflict` rather than self-deadlock. The external example's optional checksum is correctly omitted rather than set to `undefined`; its required idempotency key is intentionally present in the browser request. `js check` passed for the example with its documented capabilities and service subject. Focused race checks for JSVM, filestore, and pgbase passed without running PostgreSQL fixtures.

## P1 re-review — `ce5c59a5e03f5fa49d0ad7c73ce27940abf6f844`

The occurrence-identity fix resolves the scheduled-job P1. [`internal/jobs/service.go:196`](../../../internal/jobs/service.go:196) records the due time on an `attempts=0` claim and preserves it for retries; after successful completion the following `attempts=0` claim replaces it with the next due time. [`pgbase/jobs_options.go:36`](../../../pgbase/jobs_options.go:36) derives an idempotency-key-safe occurrence key from that durable time, and the JS payload, declaration, docs, and example use it consistently.

No actionable Standards or Spec finding remains in this narrow remediation. `go test -race ./pgbase -run 'Test.*(Occurrence|JobRun|JavaScript.*Job)' -count=1 -v` passed, as did the latest DB-free JSVM/filestore/pgbase focused race scope and example `js check`. The review did not rerun PostgreSQL acceptance because it requires the coordinated fixture; the added acceptance test directly covers commit→throw→retry one-effect behavior and a forced next occurrence with a distinct second effect.

## Integrated occurrence-key re-review — `ee806dd0221f8581838ea2d587e3f6a5691950cb`

The integrated delta preserves the occurrence-identity repair without a contract gap. The scheduler persists the due time when an occurrence begins, retains it for a retry, and only replaces it on a later `attempts=0` claim. The public Go `JobRun`, JavaScript payload, TypeScript declaration, documentation, and example expose the same retry-stable `occurrenceKey`; `runKey` remains the per-claim fence token.

No actionable Standards or Spec finding was reproduced. `go test -race ./pgbase -run 'Test.*(Occurrence|JobRun|JavaScript.*Job)' -count=1 -v`, the focused DB-free JSVM/filestore/pgbase race scope, and the example `js check` all passed. PostgreSQL acceptance was not rerun because it requires the owner/lead-coordinated fixture; its owner-reported repeated acceptance covers one effect across commit→throw→retry and a distinct effect for the forced next occurrence.
