> Agent report snapshot. Local worktree paths and raw log filenames describe the agent’s scratch evidence at review time; those raw files are not all included here. Published source identities are recorded in [the commit map](../runtime-commit-map.json), and current combined CI evidence is in [validation](../../validation.md).

# PgBase security agent evidence — 2026-11-16

## Scope and provenance

Independent agent: `/root/security`. Checkout: `PgBase-security`, branch `audit/security-20261116`, inherited runtime baseline `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b`. Original CodeQL run `43751625628`, Go job `132926640587`, scanned commit `cf50eed36e7d2f15915ade9f66a156faf34871ab`. `git diff cf50eed3 6dd8f644 -- internal pgbase migrations examples` is empty. The supplied Go artifact contains **27 results**, not zero. This agent owns 18 of those results: 1–7, 16–17, 19–27. Query/cursor/allocation results are handled separately by the performance agent.

Evidence inputs are original `evidence/go/go.sarif`, `evidence/go/findings.json`, and `evidence/codeql-go.log`, downloaded by the root agent from GitHub. Artifact `14176194587` SHA-256: `d325c391955a94a1646d3055046e40d277738f0c9b433be48c083054c89a5092`.

Read root SECURITY.md, workstream instructions, source, callers and SARIF code flows. SECURITY.md contains an obsolete exclusion paragraph claiming public authentication, files, realtime and SDKs do not exist. That paragraph is contradicted by current routes/source and must not be used to discard findings. Main agent owns correcting that scope. The supported administrator acts as installation operator; custom Go/JavaScript hooks are trusted application code. This is a scoped audit, not exhaustive whole-repository coverage.

## Original CodeQL findings, in input order

Line numbers below refer to the scanned baseline. No result has been silently dropped or reclassified as an executed zero-result scan.

| # | Rule / location | Classification | Evidence and boundary |
|---|---|---|---|
| 1 | `go/html-template-escaping-bypass-xss`, `internal/mailer/layout.go:16` | Trusted-template configuration boundary; defensive refactor implemented | `template.HTML` intentionally treats administrator-authored HTML as markup. SARIF entry is `auth_admin.go:307` preview HTML/action URL. Route is `accessAdminSession`; response is JSON. The administrator UI separately uses `sanitizeMailHtml` in `ui/admin/src/auth/mailPreview.ts`, rebuilding nodes and removing scripts/event handlers. No unauthenticated cross-user admin XSS was established. Dynamic URL interpolation nevertheless created extra elements and corrupted a link under a reproducible preview test; contextual rendering now keeps data separate from markup. |
| 2 | `go/reflected-xss`, `internal/api/compression.go:176` | False positive: impossible interface dispatch in supplied flows | All flows enter from `writeDigestField` (`sqlconsole/types.go:360–364`) or `writeDigestPart` (`recordartifact/artifact.go:175–179`). Their actual callers construct SHA-256/HMAC hash writers. SARIF substitutes an HTTP ResponseWriter implementation for that writer interface, a sink the concrete invocation cannot reach. No change made to compression semantics. |
| 3 | `go/reflected-xss`, `internal/api/custom_routes.go:228` | False positive: same impossible hash-writer dispatch | Each supplied flow passes hash bytes into `noStoreResponseWriter.Write` by the same interface approximation. Caller inspection finds only HMAC/SHA-256 writer instances in these digest helpers. Trusted custom routes can deliberately produce HTML; that is a separate application responsibility, not this trace. |
| 4 | `go/reflected-xss`, `internal/api/middleware.go:266` | False positive: same impossible hash-writer dispatch | `statusWriter.Write` is not the concrete receiver in the recorded digest calls. JSON output also sets `application/json; charset=utf-8` in `internal/api/json.go`, but the primary counterevidence is the impossible writer target, not an assumed browser behavior. |
| 5 | `go/reflected-xss`, `internal/api/schema_handlers.go:652` | False positive: same impossible hash-writer dispatch | `collectionHookResponseWriter.Write` cannot be called by the traced digest helpers with their actual HMAC/SHA-256 receivers. |
| 6 | `go/reflected-xss`, `internal/observability/metrics.go:468` | False positive: same impossible hash-writer dispatch | `statusResponseWriter.Write` cannot be the receiver in the supplied hash-writer invocations. |
| 7 | `go/email-injection`, `internal/mailer/smtp.go:146` | False positive for header / SMTP DATA injection | Constructor rejects CR/LF in From and parses its mailbox. Send rejects CR/LF in To/Subject and parses To before encoding. Attachments reject CR/LF in filename/content type. `smtp.Client.Data` returns a textproto DotWriter, which dot-stuffs body terminators. Added local SMTP fixture regression for all header controls and a forged DATA terminator. Arbitrary body text is intended message content. |
| 16 | `go/zipslip`, `internal/backup/archive.go:93` | False positive for archive path traversal; separate size-bound defect fixed | `cleanRelative` rejects absolute paths, backslashes, `..`, noncanonical paths; Unpack accepts only regular entries and manifest/database/files paths; creates a new private directory and files with O_EXCL; Verify checks inventory, sizes and checksums. Existing traversal tests pass. Adjacent cumulative byte arithmetic could overflow; see the separately reproduced defect below. |
| 17 | `go/incorrect-integer-conversion`, `internal/adminauth/mfa.go:269` | False positive for overflow; actual format bug fixed | `len(code)==6` bounds Atoi's result far inside int32. However signed `+70624` is accepted as the valid `070624` OTP. Strict six ASCII digit parsing now rejects noncanonical aliases without changing legitimate OTPs. This is not a demonstrated second-factor bypass or reduced secret search space. |
| 19 | `go/weak-sensitive-data-hashing`, `internal/adminauth/service.go:649` | False positive: comparing existing hashes / setup tokens | `constantTimeStringEqual` SHA-256-normalizes two values for equal-width comparison. Password callers pass encoded Argon2id hashes returned by `confirmCurrentPassword`, then recheck the stored hash under an account lock. Setup callers pass the setup token. Password storage and verification are Argon2id in `internal/adminauth/password.go` (64 MiB, time 3, parallelism 4, random salt); no raw password is stored as SHA-256 here. |
| 20 | `go/weak-sensitive-data-hashing`, `migrations/system/embed.go:294` | False positive: migration source checksum | Input is the embedded SQL source `adminPasswordResetsSQL`, not a user's password. SHA-256 records migration content integrity. |
| 21 | `go/log-injection`, `examples/custom-routes/main.go:37` | False positive for supplied remote input trace; example logging hardening remains optional | SARIF mixes request hook events whose Kind includes a supplied schema operation with an unrelated application start hook invocation. `beginApplicationStart` constructs fixed HookAppBoot/HookAppServe kinds; Run/Start return those specific hook errors. A schema request error does not propagate into this startup fatal path. Example uses text log.Fatal; operator or trusted hook error text is not guaranteed one line, so this is not a universal endorsement of arbitrary log sinks. |
| 22 | `go/log-injection`, `examples/jobs/main.go:39` | False positive for supplied remote input trace | Same shared-call context conflation. Application start event kinds are fixed; user request event is a different allocation/invocation. Same trusted error logging caveat as #21. |
| 23 | `go/log-injection`, `examples/lifecycle/main.go:43` | False positive for supplied remote input trace | Same request-vs-startup event conflation; RunManaged does not return arbitrary request handler errors. Same trusted error logging caveat as #21. |
| 24 | `go/log-injection`, `internal/api/middleware.go:145` | False positive: validation + structured sink | Client request IDs must be 8–128 ASCII alphanumeric/underscore/hyphen characters. Invalid IDs are replaced. Recovery logs a structured slog attribute, not interpolated record text. CLI logger uses JSON; embedded default is a discard JSON handler. |
| 25 | `go/log-injection`, `internal/cli/database.go:84` | False positive for record injection with shipped logger | `operationError` writes error as a slog field. CLI constructs `observability.NewJSONLogger` in database.go:58; JSON escapes line breaks and its ReplaceAttr redacts sensitive values. Custom external handlers are not covered by that guarantee. |
| 26 | `go/log-injection`, `internal/cli/run.go:259` | False positive for record injection with shipped logger | CLI sets NewJSONLogger at run.go:236. Errors are structured/redacted fields. Supplied trace also conflates request and application startup hook contexts as in #21. |
| 27 | `go/log-injection`, `pgbase/backup_alert.go:92` | False positive under shipped/default logger; custom logger is an operator boundary | Error goes through slog.ErrorContext attribute. CLI supplies redacting JSON; embedded default discards via a JSON handler. No raw concatenation into a log record occurs here. A custom handler that writes fields as raw text needs its own encoding/redaction contract. |

All 18 original supplied claims are `not_actionable` as the particular cross-boundary exploit alleged by the SARIF, with high confidence in the inspected concrete flows. This does **not** mean the flagged code should remain unchanged: the mail template boundary and OTP syntax were hardened, and a distinct backup resource-bound defect was found and fixed. Results are not a clean rerun of CodeQL and not a statement that the rest of the repository has no vulnerabilities.

## Reproduced defects and corrections

### Backup cumulative byte overflow — `a750f7d8`

Unpack added attacker-controlled `tar.Header.Size` to the cumulative signed int64 total before comparing with 8 GiB. A first 1-byte entry followed by a second MaxInt64 header wraps the total negative. The loop then starts reading the declared payload instead of rejecting the header. This permits malicious compressed input to bypass the intended expanded-byte quota before EOF, storage exhaustion or another check aborts it. Upload is protected by an administrator session or the pre-install setup-token guard; it is not an unauthenticated endpoint. Backup bytes remain lower-trust than the operator even when uploaded by that operator.

`TestUnpackRejectsCumulativeSizeOverflowBeforeReadingPayload` uses a tiny truncated archive, never allocates/writes the huge declared body. Baseline fails with `overflowing header limit: unexpected EOF`. Fixed code checks `header.Size > maximumUnpackedBytes-total` before adding; the test receives the quota error and confirms destination cleanup. Existing regular-file/traversal/inventory controls remain unchanged.

### Administrator TOTP canonical form — `2e76ea93`

At deterministic timestamp 1700000270 and the test-only secret, valid code is `070624`. Baseline accepts `+70624`. `TestTOTPRequiresExactlySixASCIIDigits` reproduces that failure and covers negative/sign/length/non-ASCII/NUL/large-integer forms. Fix accumulates exactly six verified ASCII digits in int32, bounded to 999999. Existing valid-code, wrong-code and encryption tests still pass. No schema/API shape change.

### Dynamic HTML mail values — `115c6fcb`

Baseline preview of a valid HTTPS URL with a quote and HTML in its query generates injected image nodes and truncates the anchor destination because substitution happened in HTML source. `TestMailPreviewTreatsActionURLAsData` fails on baseline with two injected image elements, changed visible text and changed href. Server preview is admin-only and the UI sanitizes nodes; no cross-user XSS impact is claimed.

`RenderHTML` now parses the trusted administrator fragment as a named template and exposes the existing `{{action_url}}`, `{{otp}}`, `{{otp_id}}`, `{{alert}}` placeholders as plain string values. `html/template` escapes in the actual text, attribute, URL or script context. No template.HTML data bypass remains. Preview, action mail, OTP and alert callers use this renderer. Plain-text bodies and token data remain separate and unchanged. Valid token URL fragments round-trip, and values do not become elements/attributes. Unsafe dynamic URL schemes become `#ZgotmplZ`; malformed/ambiguous HTML and unknown expressions fail with no partial output. `validTemplate` now rejects unrenderable contexts before persistence.

Compatibility: placeholder names and trusted HTML ownership are unchanged. Previously stored malformed/incomplete HTML may now fail during send; operators must repair and save the template. Intentional template markup is not sanitized by this helper. It is not permission to accept templates from public users.

## Executed local checks

Go compiler: `/workspace/scratch/2a97293516a0/toolchains/go/bin/go`, Go 1.26.11 Linux/amd64. Baseline reproduction commands ran new tests on unchanged implementations before applying corresponding fixes.

- Before fixes: `go test ./internal/adminauth ./internal/backup -run 'Test(TOTPRequiresExactlySixASCIIDigits|UnpackRejectsCumulativeSizeOverflowBeforeReadingPayload)$' -count=1` failed in both packages as described above.
- After fixes: `go test -race ./internal/adminauth ./internal/backup -count=1` passed (adminauth 4.474s, backup 1.381s).
- Before mail fix: `go test ./internal/userauth -run '^TestMailPreviewTreatsActionURLAsData$' -count=1` failed on the malicious URL case.
- Before template validator fix: `go test ./internal/userauth -run '^TestMailTemplateRejectsUnclosedHTMLContextBeforeSaving$' -count=1` failed on unclosed href/script contexts.
- After mail fix: `go test -race ./internal/userauth ./internal/mailer -count=1` passed (userauth 3.725s, mailer 1.048s).
- SMTP injection guard regression: `go test -race ./internal/mailer -count=1` passed (1.039s); test uses only a loopback fixture, not external SMTP.

PostgreSQL integration completed after repairing the two environment issues below. The successful run used Go 1.26.11, PostgreSQL 17.9 + pg_trgm, a fresh private cluster, and `go test -json -p 1 -race -tags=integration ./internal/adminauth ./internal/userauth ./internal/backup -count=1 -timeout=15m`. Runtime/tests were at `bf911d78`; the concurrent final metadata-only `b8d5f40b` reclassifies the existing x/net dependency without changing code, tests, versions or checksums.

| Package | Result | Time |
|---|---|---:|
| internal/adminauth | Pass | 69.964s |
| internal/userauth | Pass | 101.141s |
| internal/backup | Pass with existing S3 cases excluded | 4.612s |

JSON event totals: **237 top-level tests passed**, 4 top-level S3 tests skipped; **409 passing test events** including subtests, 6 skipped S3 test events, **zero failures**. Evidence: `security-integration.jsonl` and `security-integration.summary.json`. The disposable PostgreSQL server was stopped when the command exited successfully. These times are test durations, not performance measurements.

Skipped cases (unchanged existing checks require `PGBASE_TEST_S3_ENDPOINT`, which was not configured):

- `TestOnlineExtensionObjectSnapshotRestoresIndependentBytes/s3`
- `TestOnlineSnapshotRetainsConcurrentDatabaseAndObjectState/s3`
- `TestS3FileSnapshotRestoreNewThenStorageResetPreservesIndependentLocalBytes`
- `TestS3RepositoryPublishListStreamAndDelete`
- `TestManagerCreatesAndRestoresThroughS3`
- `TestBackupStorageControllerS3SettingsProbeAndTransitionFence`

The corresponding local-storage snapshot/restore paths ran successfully. This is not S3, full CI, browser E2E or external provider evidence.

## Independent cross-review

Performance agent statically reviewed the three security commits. No blocking issues found: digit bound <=999999, tar subtraction protected by prior invariant/negative-size rejection, all WrapHTML call sites migrated, valid-template syntax checked before persistence. Reviewer emphasized migration behavior for previously stored malformed HTML, recorded above.

This security agent independently inspected performance `d0322ace` cursor bounds and query codec optimization. No blocking issue found. All six fieldsByOrdinal call sites only read the slice; unordered collections retain a sorted copy; comparator exactly matches the previous ordinal/name order; scan preallocation retains file/multiple-relation skips and index alignment. Query catalog snapshot semantics remain intact. Cursor v5 compares both low and high 32-bit halves, so the narrowing warning does not establish full-width equality bypass. Independent `go test -race ./internal/cursor ./internal/query -run 'Test(CursorEnvelopeSizeBoundary|V5DescriptorMatchesCatalogVersionAtFullWidth|EncodeRecord)' -count=1` passed (cursor 1.038s, query 1.085s).

Also inspected scalar root-page byte measurement caching: both root-plan decoders initialize root/wire to the same fresh map, no callbacks can modify that map before assembler.finish when hydrate is nil, and a nonnil hydrator returns into the separate sequential path. The new hydration/NaN regression checks that changed hydrated maps still receive final validation. No blocking issue found.

Independently reviewed and executed the root agent's new `scripts/summarize-codeql.py` against both original SARIF files. Correctly recovered 27 Go results (15 error, 12 warning) and 3 JavaScript results (2 error, 1 warning), with zero missing security-severity metadata. All eight then-current parser tests passed. Suggested one optional robustness check for conflicting nested/legacy rule indexes; root agent owns that follow-up. This validation does not constitute rerunning CodeQL.

### Environment failures preserved

The first PostgreSQL attempt used a server started by a different exec command and failed connection-refused: this executor isolates process/network lifetimes. The second single-exec attempt reached PostgreSQL but could not apply migration 66 because the freshly built PostgreSQL prefix lacked pg_trgm. It was interrupted after diagnosis (exit 130). Logs are `security-integration-environment-failed.log` and `security-integration-missing-pg-trgm.log`. Neither is a product assertion failure or a successful integration run. The third attempt used a fresh private data directory, pg_trgm installed, and PostgreSQL start/test/stop inside one exec; it passed with the explicit S3 exclusions above.

`bf911d78` adds explicit SMTP injection boundary tests. `b8d5f40b` moves the already pinned golang.org/x/net v0.62.0 from indirect to direct because tests now parse HTML; no version/checksum change. `go mod tidy -diff` then passes with no differences.

## Remaining limitations

- No arbitrary SMTP/OAuth provider, Windows filesystem or production restore testing in this agent's local run.
- No claim that custom administrator templates, trusted hooks, or custom logging handlers can safely be treated as untrusted code.
- The backup fix tests rejection before body reads; it deliberately does not decompress a multi-GiB bomb or fill the disk.
- Broader authorization, realtime, SQL, query/resource and frontend/SDK coverage belongs to other workstreams, not implicitly to this scoped report.
