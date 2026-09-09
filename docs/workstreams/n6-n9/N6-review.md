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
