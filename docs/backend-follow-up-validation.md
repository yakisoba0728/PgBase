# Backend follow-up validation — 2026-11-13

This follow-up reviews the PostgreSQL rewrite at `1f93b15a` and fixes reproduced correctness defects found in the shared working tree. It adds no dependencies or schema migrations. Independent source reviews supplemented package owners' regression tests; they are not evidence of exhaustive security coverage.

## Changes

- Authentication: refresh reuses its existing transaction connection; auth-record deletion locks and evaluates the current deletion policy in the deletion transaction. Administrator password replacement also invalidates pending reset links and email OTPs in the same transaction.
- Records: public-identity collisions no longer abort transactions before a retry; truncate resolves public record IDs; numeric modifiers reject invalid decimal syntax and preserve the exact-string transport requirement for mixed direct assignments.
- Rules: quantified comparison inversion preserves direction; contains SQL uses valid PostgreSQL escaping; in-memory LIKE matching avoids recursive exponential splitting. Auth-left and literal-left contains expressions that cannot be represented without reversing their meaning now fail closed.
- API: marked impersonation sessions cannot use generic auth-record password/email/verified/active updates or MFA/external-auth deletion to bypass dedicated credential protections; ordinary user operations retain their existing behavior. Service and anonymous CRUD/batch routes now receive the same lifecycle hooks and runtime batch settings as administrator/user routes. Expanded records apply the same relation-target visibility filtering as direct reads. Batch hooks are rechecked against the configured operation limit and HTTP result statuses follow the final operations, preventing a response panic after committed writes.
- Async work: impossible cron schedules are rejected, SMTP envelopes use parsed mailbox addresses, outbox failures release remaining claims, and cancellation from the last Before hook stops the operation.
- Files and backups: storage namespace identity ignores transport-only/irrelevant options, preventing cleanup from deleting objects in the still-active namespace. Offline local-only backup commands inspect persisted storage and reject effective S3. Pack preflights restore entry-count, manifest-size and unpacked-size limits before publication/pruning. Local backup publication does not overwrite an existing archive; canceled operations cannot publish; source-storage cleanup failures use the existing retry/backoff state. Lease-loss regression tests wait for backend termination rather than racing asynchronous lock release.
- Schema and migrations: tightened JSON field-size checks fail on oversized existing rows; generated Go package names reject keywords; bounded migration-source errors remain valid UTF-8.
- JavaScript: JSON conversion preserves depth/cycle checks, treats imported `__proto__` as an own property, and uses an internal symbol for exact-number branding. Canonical declarations and generated SDK artifacts now describe template rendering, actual object-list property casing, and exact filesystem mode numbers.
- SDKs: TypeScript and Dart bound clean-EOF realtime retries, honor cancellation before sending requests, and preserve newer authentication state when older requests complete. Both shared realtime clients stop old-session events after auth changes, including a change triggered by an earlier listener in the same delivery frame. TypeScript also closes cancellation/listener gaps and rejects empty record identifiers. Generated TypeScript distributions are included.
- Admin UI: boolean false values remain explicit during create; ambiguous create and schema-import responses retain recovery identities. Generated embedded assets are rebuilt and included.
- Documentation: advanced field documentation now lists supported modifiers and the multipart batch file path accurately.

## Compatibility

No runtime HTTP endpoint or database schema was added. Previously accepted auth-left/literal-left contains access rules now return an invalid-rule error instead of evaluating swapped operands. Rephrase such rules with a supported field-left predicate. Generated native and legacy JavaScript declarations share the corrected native API types.

## Validation

- Go 1.26.11: full unit suite with `-race` and the repository skip policy; pinned blocking lint; `go vet ./...`; formatting and diff whitespace checks.
- Owner integration suites: PostgreSQL 17 and local S3, covering auth, identities/mutations, rules/query/projection, schema/migrations/membership/settings, files/backup/doctor, jobs/mail/realtime/lifecycle, JavaScript/CLI/public-site/SQL-console.
- API regression: real PostgreSQL commits UPDATE+CREATE after a batch hook rewrites one request, returning matching HTTP statuses and exactly two committed records.
- Lost staged-lease regression: race detector, 30 repetitions, with synchronized backend termination.
- SDK: final TypeScript build/strict types/fixtures and 53 Node tests; 69 Dart tests and clean analyzer; four live SDK HTTP/realtime/admin-backup/text-ID conformance tests; generated JavaScript declaration parity and strict TypeScript compilation.
- Admin UI: Node 24.22.2 and Node 26.13.0, 205 tests, type checking, and embedded build passed.

Final frozen Go unit/race, lint and vet passed. After the late impersonation guard, the full API unit/race suite passed again (20.193s), with lint and API vet clean. The late backup/file/CLI/query/accessrule integration race checks passed. API full PostgreSQL integration passed (34.925s).

The full pgbase PostgreSQL suite did **not** pass: it hit the deliberately bounded five-minute run budget and reported `TestExtensionObjectReplacementPublicationFailurePreservesCommittedBytes` at `extension_objects_integration_test.go:115` with one physical version rather than the expected count. A cleanup timing explanation is unverified; the failure was not rerun to hide it. The first aggregate attempt also lacked pg_dump in PATH; the final attempt used the existing local PostgreSQL-tool wrappers. These failed/incomplete aggregate runs are preserved separately from successful focused checks. Local logs, fixtures, credentials, and generated scratch reports remain excluded from Git.

## Remaining limits

- Dart projected authentication refresh can rotate a single-use token before a partial response fails complete-session decoding. Default unprojected refresh is covered; projected-session behavior still needs an explicit contract decision.
- Native record-event SDK helpers can send `fields`, while the backend rejects a nonempty query string on that stream. Shared realtime projection is covered; native stream projection remains unresolved.
- Managed restore records an active target that database CLI commands do not consistently resolve. Commands using an unchanged environment may target the preserved original database.
- Official distroless runtime images do not include PostgreSQL backup/restore client tools. Managed backup enablement in those images still requires a supported runtime-tool strategy.
- Configurable user-auth rate policy does not consistently reach all auxiliary OTP/reset/email/OAuth/MFA paths; this is a source-review candidate, not a reproduced vulnerability.
- No production SMTP/OAuth provider, external TLS/proxy deployment, or sustained load benchmark was exercised. Security review findings and any unvalidated candidates are reported separately.

## Additional source-review follow-ups

The independent reviewer widened its scope beyond this change. The following are source-traced candidates, not completed runtime reproductions or an assurance that every path was reviewed. They remain open unless explicitly listed as fixed above. Priority labels in the scratch review are provisional.

| Area | Candidate requiring validation |
| --- | --- |
| Text identities | Create/update followed by delete in one batch may lose its response projection mapping; delete realtime replay may also require a mapping that deletion removed. |
| Relation queries | Target view rules may lose HTTP request context during relation filter/sort; metadata-only file fields may be selected as physical columns; text-target relation modifiers may mix public and internal IDs. |
| Schema preflight | Boolean required-mode migration may reject true values; required tightening in an existing PocketBase validation mode may leave blank values. |
| Rule validation | Invalid contains operand types and UUID/time literals may be accepted until SQL execution; a generated relation alias may shadow a valid root table name. |
| Pagination | Modifier/JSON-path cursor descriptors may reject valid filters or omit path identity; requested PocketBase page sizes above the execution cap may skip rows. |
| User authentication | In-flight KDF operations may commit stale realm policy; unlink fallback may count a realm-disabled password; credential changes may leave pending MFA challenges; paired auth relation targets may be double-counted; authorized updates may use a rule captured before a row-lock wait. |
| JSON input | Shared profile/auth-record decoding may round large JSON numbers in paths that do not retain json.Number. |
| JavaScript | Filesystem writers may concatenate padded base64 incorrectly; filesystem-only upload may use the OS capability; FieldsList array species may break inherited operations; a throwing result getter may escape codec error translation. |
| SMTP | Attachment base64 lines may exceed MIME line-length limits. |
| File variants | Thumbnail creation may race deletion and create an object after deletion inventory. |
| Schema imports | Companion-index capability metadata may remain stale; rules/access-only imports may not be recorded as migrations. |
| Realtime | Invalid user bearer authentication may fall through to a nil service-token dependency in custom router construction. |
| Direct embedding | Batch/Truncate created without an injected membership runtime may exhaust a one-available-connection pool. |

These candidates need owning tests and review before they can be counted as resolved. A separate security audit and frontend review were still running during integration; their final results must be considered before authorizing a merge.

## Merge gate

**Keep the PR open: an existing nested auth-relation authorization bypass remains unfixed.** A safe, isolated PostgreSQL overlay regression reproduced that `organization.owner.id = @request.auth.id` can evaluate true for an unrelated actor when nested single-auth-relation queries reuse `aru`/`arp` aliases. The affected `internal/userauth/auth_rule_resolver.go` source was not modified after freeze. This proves incorrect authorization-engine evaluation; a complete HTTP account takeover was not executed. Correct scoped aliases and negative list/view/update/manage regressions are required before merge.

The separate read-only security audit additionally identified source-traced concerns involving authorization-before-KDF admission, stale verified context in SSE, catalog relation alias capture, factor guessing budgets across MFA challenges, and pending MFA challenges after generic credential replacement. Runtime exploitation was not demonstrated for those additional items. The late impersonation HTTP guard is independently covered by failing-before and passing race-tested regressions; it does not close unrelated domain authorization findings.

Frontend review also left unimplemented candidates involving edits made during save/refetch, unsaved navigation guards, one-time service-token presentation, auth-profile JSON number handling, provider/session response ordering and mobile focus behavior. The three fixes above do not claim to resolve the wider frontend review.

## Candidate artifact

A new arm64 candidate was captured from the frozen runtime/UI working tree at 14:08:23 UTC, with source manifest SHA-256 `cd4f4a72623b891af289054a1850edda5863b78c31bcd1e56da6ccab49f0df09`. Its image digest is `sha256:aa4b827c7014a81a7a306098daface8ae8359b7d9e16c23a449c9073387dd070`. This is a worktree-manifest build, not an assertion that the old HEAD already contained the follow-up changes.

**The candidate release gate is BLOCKED by the unresolved nested auth-relation bypass.** Building an image or passing artifact checks does not authorize release, promotion, deployment, or merge. The candidate was not promoted to the established local candidate tag.

Candidate artifact checks completed successfully: digest-pinned E2E twice with shuffle (76.695s), PostgreSQL 16/17/18 release-image restore (67.549s), Trivy HIGH/CRITICAL count zero, SPDX SBOM with 57 packages, and byte-exact comparison of all 144 embedded UI files. The runtime/UI build-input manifest remained unchanged after capture. These artifact checks do not close the authorization bypass or the failing/incomplete full application suite.

Local evidence location: `.local/redesign/sprint/candidate/` (`candidate.json`, `e2e.log`, `restore.log`, `security-image.json`, `sbom.spdx.json`, `embedded-ui.json`); aggregate logs: `final-unit-race-frozen.log`, `final-lint-post-guard.log`, `final-vet-frozen.log`, `final-api-unit-race-final.log`, `final-late-integration-race.log`, and `final-app-api-integration-frozen.log`. Local evidence and fixtures are deliberately excluded from this commit.
