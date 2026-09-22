# Continuation verification — 2026-09-22

This record covers the second native product slice on `codex/remaining-development`, based on the user's selected local `origin/main` snapshot `6993744a8f36db48ddfeaa8dae017656752d24c7`. The complete worktree also contains the preceding development and correctness slices; those changes were preserved. No commit, merge, installed-project replacement or public deployment is implied.

## Delivered behavior

- [Query continuation](continuation-query.md): bind both field operands in cursors, preserve ordinary literal/function signatures, and reject unsupported field operators/types before PostgreSQL execution.
- [Administrator accounts](continuation-administrators.md): bounded list/create/activation/password replacement/session revocation through native API and UI; protect current/last active administrators and preserve TOTP/IP policy.
- [Administrator concurrency](administrator-credential-concurrency.md): management, reset and OTP transactions use consistent lifecycle ordering, prevent stale issuance after deactivation and preserve database cancellation errors.
- [Signup profiles](continuation-signup.md): optional typed profile inputs for password and new OAuth signup, precise numbers, transactional constraints, and existing extension hook admission.
- [UI](continuation-ui.md): complete native auth-policy form and administrator management, including immediate local session clearing after successful own-account revocation.
- [SDK](continuation-sdk.md): profile input options, realtime status/error observation and explicit cursor reset; isolate consumer exceptions and preserve coherent status ordering under reentrant callbacks.

## Tests and review

| Area | Executed result |
| --- | --- |
| Entire Go unit suite | `scripts/test-go-unit.sh -race -p 2 -count=1 -skip '^TestRuntimeStructuralBudgetsDoNotDrift$'` passed with the skip guard. The Windows Git-dependent structural check was executed separately below. Linux execution dropped `CAP_DAC_OVERRIDE` so permission tests were meaningful. |
| Query integration | Full `internal/query`, PostgreSQL 17.9, race detector and integration skip guard: 92.299s. This is after final field-operator validation. |
| Administrator integration | Full `internal/adminauth`, PostgreSQL, race detector and skip guard: 26.791s. Includes account management and five reproduced credential concurrency/error cases. |
| User authentication integration | Full `internal/userauth` with PostgreSQL and race detector: 29.276s. |
| HTTP API integration | Full `internal/api`, PostgreSQL, race detector and skip guard: 24.542s. |
| App integration | Final complete `pgbase` rerun with PostgreSQL/MinIO fixtures, race detector and integration skip guard: 195.244s, after lifecycle-wrapper correction. |
| Final hook unit regression | `go test -race ./pgbase -count=1`: 2.564s, after wrapper correction. Hook/profile focused HTTP matrix also passed in 3.995s. |
| UI | 50 files / 624 tests, typecheck and Vite build passed. Existing test `act`/jsdom warnings and the large-chunk build warning remain visible; they were not suppressed. |
| Actual browser | Fresh rebuilt embedded UI/server, Chromium, all three continuation specs: 8 passed in 13.7s. Includes two new administrator/policy flows and six preceding record/rule/view/identity flows. |
| TypeScript SDK | Complete `npm test`: build, source/generated public type checks, conformance and 37 Node test entries passed. |
| Dart SDK | Pinned Dart 3.13.0 image: analyze found no issues, all 56 tests passed. |
| Static/repository checks | Final `go vet ./...`, changed/new Go `gofmt`, `git diff --check`, and `python docs/check-status-index.py` passed. |

The first complete app integration run caught a real regression: `TestAuthLifecycleNamedRealmVetoAndNondisclosingRequests` reported a vetoed signup succeeding. Additive methods promoted through an embedded service bypassed its lifecycle wrapper. This was reproduced before fixing explicit wrapper overrides, then covered for default/named/OAuth routes with absent, empty and populated profile inputs. The initial failing run is not counted as a pass.

Independent reviews also found incomplete field-comparison type/operator checking, administrator pending-credential lock inversions/late issuance, and realtime observer reentrancy/pre-aborted backoff handling. Each finding received a failing regression and a targeted passing result. The reviews do not constitute exhaustive concurrency, security or production-scale proof.

## Exact source accounting

All new files were included using a uniquely named temporary Git index. The real index remained unchanged. `tools/repostats --compare 6993744 --pretty` used the existing source classifier without exclusions or spare allowance:

| Measure | Final value |
| --- | ---: |
| Handwritten Go + UI runtime | 105,112 lines |
| Go runtime | 93,955 |
| UI runtime | 11,157 |
| Compared with selected base | +2,230 / −464, net +1,766 |
| Compared with prior 104,092-line checkpoint | +1,020 (Go +795, UI +225) |
| Data-group runtime | 28,469 |
| Query runtime | 7,875 |
| Go interfaces / exports / constructors | 100 / 1,586 / 64 |
| UI direct page state/effect calls / exports | 190 / 317 |
| Runtime files above 800 lines | 0 |

Four optional transport interfaces preserve older injected auth implementations. New service and lifecycle wrapper boundaries, administrator page state, and typed signup/query validation account for the increase. SDK code and generated assets retain the repository's existing separate classification; the Go/UI runtime figure is not a claim to count every source language. Exact Windows structural/data-group tests passed in 0.836s after setting the measured ceilings.

## Operational boundaries and remaining work

There is no new database migration in this slice. Affected old field-comparison/contains cursors require restarting the traversal; consult the query record before a mixed-version rollout. `resetCursor` ends subscriptions and discards the cursor; application snapshot reconciliation remains the caller's responsibility. Signup profile rules use the current candidate-field grammar; nested request-body profile selectors are not added. Actual OAuth codes may need fresh authorization after a rejected signup even when PgBase rolls its transaction back.

PostgreSQL 17.9, MinIO and local mail/OAuth fixtures provide reproducible local evidence. Real provider credentials, Windows self-update execution, installation-copy upgrade/rollback, multi-process load/failure acceptance and public release remain separate work. Next local design/development choices include literal-left `?` operator normalization, builder-view import updates and chosen image/HTTP Range behavior. These are native product decisions, not an automatic PocketBase parity backlog.

The browser fixture's temporary administrators were deactivated and temporary realms removed. After verification, only the task-owned `pgbase-remaining-browser`, `pgbase-remaining-runner`, `pgbase-remaining-development-postgres-1` and `pgbase-remaining-development-minio-1` containers were stopped. Unrelated running containers and the existing installation were preserved. Changes remain local and uncommitted on `codex/remaining-development`.
