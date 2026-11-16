# Final continuation: security evidence and remaining boundaries

This review starts from PR #148 source `b376e36697674544be821be0290e24916d81da7d`, independently of the earlier success summary. It records the security agent's inspected call boundaries, reproducible changes and gaps. It is **not an exhaustive certification of every repository file**, and it does not replace qualification of the final integrated commit.

The work used an isolated Git worktree, ephemeral keys and local fixtures. No production service, database, credentials, release or package publication was involved. Security changes introduce no database migration; system schema remains 67.

## Reproduced changes

### Archive trailing-member inflation

`internal/backup/archive.go:Unpack` previously drained the gzip reader after the tar terminator to validate the compressed trailer. A trailing gzip member was already invalid, but the drain expanded its entire contents before rejecting it. Entry-size accounting did not cover those bytes.

`TestUnpackRejectsTrailingContentWithoutDrainingIt` appends a small, two-MiB expanded member and a sentinel reader error to a valid backup. The baseline reached the sentinel, proving it consumed the invalid member; the correction stops after the first extra uncompressed byte. A valid archive still reads to gzip EOF, which validates its checksum. Corrupt checksum, cancellation, duplicate/traversal rejection and failed-staging cleanup remain required. Commit `fc5f347e` implements the correction.

The affected boundary is administrator/setup-authorized backup input. This is resource amplification within a privileged import operation, **not evidence of an unauthenticated public upload or a path traversal bypass**. Raw initial failures are retained in [failing-before.log](evidence/final-security/failing-before.log).

### Archive metadata accounting

Source inspection found a second accounting boundary: Go's `archive/tar.Reader.Next` consumes local PAX and GNU metadata internally before returning the next regular entry. Those bytes do not appear in PgBase's regular-entry count or declared payload-size sum. The first trailing-member fix therefore does not establish a bound on all decompressed bytes.

A bounded regression uses repeated local PAX headers before an otherwise valid backup. It scales the private extraction budget to a few KiB so no multi-GiB expansion is necessary. Before the correction, extraction accepted that over-budget archive; Pack and Unpack also accepted a stream one byte over the scaled limit. The private helper extraction preserved public behavior while allowing the same limit arithmetic to be tested at a safe scale. [metadata-failing-before.log](evidence/final-security/metadata-failing-before.log) records the actual failures.

Commit `4730e549` bounds the entire decompressed tar stream, including headers, padding and terminators. A sentinel byte distinguishes budget exhaustion from validated gzip EOF. `Pack` applies the same stream limit so successful output remains restorable under that limit. Exact-budget and one-byte-over-budget tests, metadata amplification, corrupt CRC, cancellation, traversal/duplicate rejection and staging cleanup all pass in the full affected-package race run.

The eight-GiB expanded limit now includes framing rather than only regular-file payloads. Payloads very close to the old limit must leave room for metadata; generation can return a size-limit error. The two-GiB compressed limit, 100,000 visible-entry limit and 16-MiB manifest limit remain. No eight-GiB archive was expanded as part of this regression.

### OIDC time and signing-key constraints

The generic HTTP OAuth provider's ID-token fallback accepted correctly signed tokens without enforcing `iat` or `nbf`, and selected RSA keys without checking declared `use`, `alg` or `key_ops`. The fallback applies only when a provider has a configured JWKS endpoint and no user-info endpoint.

Commit `29272aff` requires numeric `exp` and `iat`, enforces optional numeric `nbf`, rejects inconsistent time ordering, preserves fractional NumericDates and uses a sixty-second allowance for future `iat`/`nbf`. Expiration retains a strict deadline. RS256 remains the only accepted signature algorithm; eligible keys require at least a 2048-bit modulus and an odd exponent of at least three. Declared key purpose and algorithm must authorize signature verification. Follow-up `a002599c` distinguishes omitted optional JWK fields from explicit null/empty values and restricts `key_ops` to nonduplicated signature operations including `verify`.

The initial signed-token fixture reproduced nine invalid acceptances before the change: future/malformed/null `nbf`, missing/future/malformed `iat`, encryption-only use/operations and a contradictory algorithm. Every token is signed with the local configured issuer's real ephemeral key, with valid issuer/audience/nonce. This establishes validation defects and stricter provider contracts. It **does not demonstrate forging an issuer signature, stealing a nonce, substituting an arbitrary issuer, or taking over an unrelated account**.

Normative and policy distinctions were checked against primary sources:

- [OpenID Connect Core, sections 2 and 3.1.3.7](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation) requires `exp`/`iat`; the acceptable age/range for `iat` is client policy. Sixty seconds is PgBase's policy, consistent with the existing Apple implementation, not a standard-mandated constant.
- [RFC 7519, section 4.1.5](https://www.rfc-editor.org/rfc/rfc7519.html#section-4.1.5) requires enforcing a supplied `nbf` and permits a small clock allowance.
- [RFC 7517, sections 4.2–4.4](https://www.rfc-editor.org/rfc/rfc7517.html#section-4.2) defines optional key-purpose/operation/algorithm constraints, disallows duplicate operations and requires consistent `use`/`key_ops`. The constrained PgBase profile permits `verify` or `sign,verify` when `key_ops` is supplied.
- [RFC 7518, section 3.3](https://www.rfc-editor.org/rfc/rfc7518.html#section-3.3) requires RSA keys of at least 2048 bits for this signature family.

The existing `oidcAudience` already checks a supplied `azp` against the client even when `aud` is a single string. No new `azp` fix was necessary. An invalid eligible duplicate `kid` currently stops lookup; this fails closed and can cause availability problems for malformed issuer key sets, but is not an authentication bypass. Real external provider interoperability was not exercised.

## Local verification and independent review

The initial correction passed:

```sh
GOMAXPROCS=2 go test ./internal/userauth ./internal/backup \
  -run 'TestOIDC|TestHTTPOAuthProvider|TestArchive|TestUnpack|TestPack|TestVerify' -count=1
```

Observed package results were `userauth 0.327s`, `backup 0.067s`. This result applies to the first two commits, not subsequently edited code.

The final security runtime, `4730e549` (containing all four security commits), then passed `GOMAXPROCS=2 go test -race ./internal/backup ./internal/userauth -count=1 -v`: **70 top-level tests, 177 passing events including subtests, zero skips**, with package times 1.642s/1.866s. [package-race.log](evidence/final-security/package-race.log) contains the full output. This is the full untagged test suite in those two packages, not the repository-wide or PostgreSQL integration gate. Go was 1.26.11 on linux/amd64. A preceding focused OIDC race run also passed and is retained separately.

The backend agent independently reviewed all four security commits and found no blocker. It specifically rechecked the valid-EOF checksum path, first-extra-byte rejection, total expanded limit, context/input limits and staging cleanup. The frontend agent separately inspected the OIDC follow-up and traced `NewHTTPOAuthProvider → Exchange → verifyOIDCIDToken → identityFromFields → CompleteOAuth`, finding no blocker. Both reviewers distinguished malformed signed issuer responses from arbitrary signature forgery. The backend agent independently executed focused archive/OIDC race regressions three times on the final four-commit runtime: both packages passed (2.019s/2.083s); its independently produced log is retained in [independent-security-race.log](evidence/final-security/independent-security-race.log).

Security-agent cross-check executions also passed without skips:

| Target | Independent execution | Evidence |
| --- | --- | --- |
| Jobs `d66f3d28` + `90d61de7` | Three top-level / 15 passing events, race + integration on isolated PostgreSQL 17.9 | [jobs log](evidence/final-security/independent-jobs-race.log) |
| Realtime retention `6d0299c0` | Three integration regressions under race on isolated PostgreSQL 17.9 | [retention log](evidence/final-security/independent-realtime-retention-race.log) |
| TS Realtime + SSE `97e7f7e` + `440466b2` | Four targeted runtime tests on Node 24.22.1 | [TypeScript log](evidence/final-security/independent-typescript-realtime.log) |
| Dart Realtime `97e7f7e` | Two targeted tests on Dart 3.13.3 | [Dart log](evidence/final-security/independent-dart-realtime.log) |

The first PostgreSQL startup attempt failed before testing because Unix-domain sockets are disallowed in this executor. TCP-only configuration resolved that environment restriction; each start/test/stop stayed within one command lifetime and removed its private data directory. This is not a PostgreSQL 16/18 or production-environment validation claim.

The security agent independently source-reviewed:

| Implementer / revision | Actual review boundary | Result and limits |
| --- | --- | --- |
| Backend `d66f3d2828f2a10db27379239c95a5aca5027157` | `internal/jobs/service.go`, `management.go`, management fencing tests; claim → handler → completion; API/extension/backup scheduler callers | No blocker. Accepted management changes invalidate old claims; completion also requires an unexpired lease. External effects remain at least once and need application idempotency. Preserving attempt/logical schedule identity across operator edits deliberately retains the outstanding logical occurrence. |
| Backend `6d0299c02f7a5b0d0e7bb7b9e20094c0479e0a9f` | `internal/realtime/outbox.go`, retention tests, `ReadFor`, replay-floor schema | No blocker. Deletion scans the first bounded sequence window and stops before an unexpired event; shared advisory lock serializes publication/pruning, repeatable-read replay sees a coherent floor. One-second transaction context includes lock wait; existing rollback cleanup can add its separate bounded time. Future-dated prefix events intentionally retain later events. |
| Frontend `97e7f7e8798fc8114af90ad35c43fb7dd2d9f8eb` | TS/Dart Realtime subscription, abort/wait/reconnect, auth-generation changes, listener set and disposer ownership; AuthStore notification semantics | No blocker. New checks stop pending subscriptions adopting a later identity and prevent an old disposer removing a new session's callback. No claim that already delivered data can be revoked. |
| Frontend `440466b2f1ab350ae056d252874d92ef82bf25c4` | TS SSE reader cleanup and malformed-frame reconnect | No blocker. Explicit reader cancellation ends the unread response before releasing its lock; cleanup failure preserves the original error. Early-consumer-exit and malformed-frame reconnect regressions independently passed. |
| Root open-loop HTTP qualification harness | Scheduled arrivals, bounded queue, worker/result accounting, timeouts, assertions, whole-owner traversal and resource denominators | Initial review requested explicit unscheduled errors, strict cursor types and mutation/final-owner assertions. Rechecked corrections: no blocker. Go process counters include generator/client/server; they are not isolated server RSS or database CPU. Errors/incomplete work must fail independently of latency. |
| Root repeated HTTP workflow/scripts | `scripts/{repeat_http_regression,analyze_repeated_load}.py`, `.github/workflows/performance-regression.yml` | No launch blocker. Historical/common harness, ABBA order, paired run-level bootstrap, fail-closed incomplete ledger and explicit broader final-runtime comparison inspected. A successful experiment job is not an absence-of-regression assertion; practical ±10% equivalence must not hide directional point estimates/CIs. Build-failure evidence capture was suggested separately. |

These were actual separate agents inspecting source. Parallel CI jobs are not counted as independent review.

## Rechecking the previous full-source scanner artifacts

The agent opened and parsed the checked-in original ZIPs, recomputed their hashes and read the SARIF flows; it did not infer findings from the old prose counts. [baseline-artifact-recheck.json](evidence/final-security/baseline-artifact-recheck.json) preserves exact rules, locations, relevant flow endpoints, extension names, hashes and image records.

| ZIP | Bytes | SHA-256 | Observed results |
| --- | ---: | --- | ---: |
| `qualification-final/go.zip` | 91318 | `28d501b9c2d3933603c17ce1fdbfb471619a43584de88358c5bafd13ac6cf472` | 24 |
| `qualification-final/js.zip` | 121675 | `82f058edf09a92ca6f14ec56dced534ad9d4515bf317bdeaea478a88ffece812` | 1 |
| `qualification-final/image.zip` | 15493 | `9b88e893bcc7ce8a1e27f537d8e3fa3c4a06f50e71fa0b1e5da14b6b4838f769` | 32 all-severity; zero HIGH/CRITICAL |

The source SARIF lacks incremental-mode/PR-diff-range restriction and matches the stored full-source provenance. These artifacts belong to the **previous** qualification runtime `5e4b71d1`, run [43780150632](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632). A local hash match verifies the stored bytes; it is not a fresh GitHub download, current-image rebuild or current-head scan.

### Independent classification of all 24 Go / one JS locations

Numbers below are one-based positions in the previous Go SARIF. Source paths identify the exact reviewed sinks; the machine-readable recheck retains the original line numbers and flow boundaries.

| Results | Rule / files | Independently checked reason and remaining condition |
| --- | --- | --- |
| 1–5 | Reflected XSS: `internal/api/{compression,custom_routes,middleware,schema_handlers}.go`, `internal/observability/metrics.go` | Reported flows cross generic `io.Writer` interfaces from `recordartifact.writeDigestPart` or `sqlconsole.writeDigestField` into HTTP writer implementations. Concrete callers construct SHA-256/HMAC writers. No such response-writer call was found in those paths. Normal API output also goes through JSON content type/encoding and security headers. This does not certify arbitrary custom-route HTML. |
| 6 | Email injection: `internal/mailer/smtp.go` | Read message construction, mailbox/header rejection and the SMTP DATA writer. Header values reject CR/LF; MIME fields are structured; `net/smtp.Data` supplies dot stuffing. Dynamic mail body data is expected. No header or DATA-terminator injection established in this reported path. |
| 7 | Allocation arithmetic: `internal/query/root_page.go` | `normalizedFetchLimit` caps pages at 200; batch capacity is derived from remaining bounded page slots. Subsequent byte caps are separate from integer capacity safety. |
| 8 | Allocation arithmetic: `pgbase/extension_objects.go` | `List` rejects limits outside 1–1000 before `limit+1` allocation and uses bound SQL parameters. |
| 9–10 | Allocation arithmetic: `internal/query/compiler.go` | `decodeInput` rejects unknown fields before the flagged allocations; successful decoded entries are constrained by the loaded schema. HTTP body limits are route-declared. Direct trusted Go callers are not certified for unlimited memory usage. |
| 11 | Allocation arithmetic: `internal/schema/operation_cursor.go` | Cursor encoding uses fixed UUIDs and canonical timestamps with year checks before adding a fixed SHA-256 signature. Decode enforces token length/canonical encoding/HMAC/owner/expiry. |
| 12–13 | Allocation arithmetic: `internal/schema/{schema_document,schema_policy_checksum}.go` | Inputs are materialized collection slices/maps; import endpoints require administrator access and cap documents at the catalog transport bound (16 MiB plus framing). No realistic integer-overflow route established. Large catalog planning remains an operational resource concern, not a claim of constant-cost schema work. |
| 14 | ZipSlip: `internal/backup/archive.go` | `cleanRelative` rejects absolute, backslash, noncanonical and parent-traversal paths; fresh private destination, regular-only entries, exclusive creates and verification constrain extraction. Separate decompression issues were found and tracked above; dismissing this traversal flow did not dismiss archive risk. |
| 15 | Integer narrowing: `internal/cursor/v5.go` | `V5Descriptor.Matches` checks both 32-bit halves of the 64-bit catalog version before acceptance; the narrowing does not discard the upper half. |
| 16 | Weak hash: `internal/adminauth/service.go` | `constantTimeStringEqual` hashes comparison operands to fixed length before constant-time comparison. Password storage/checks use Argon2id in `internal/adminauth/password.go`; this helper is not a password-storage hash. |
| 17 | Weak hash: `migrations/system/embed.go` | SHA-256 covers embedded public migration SQL for integrity/version identity. It is not used to store a password. |
| 18–20 | Log injection: `examples/{custom-routes,jobs,lifecycle}/main.go` | Trace conflates request lifecycle `Begin` calls with application boot/serve hooks. Actual boot/serve event kinds are constants and occur before serving requests. The examples' `log.Fatal` is not a demonstrated HTTP-request-to-startup-error path. Trusted custom Go hooks remain responsible for their messages. |
| 21 | Log injection: `internal/api/middleware.go` | Recovery logs a request identifier, validated/generated by middleware, without logging the panic payload. The standard logger is structured JSON. Trusted handlers that manually replace response headers are outside this claim. |
| 22–24 | Log injection: `internal/cli/{database,run}.go`, `pgbase/backup_alert.go` | Actual CLI/runtime logger construction uses `observability.NewJSONLogger`, escaping line breaks and redacting string/error attributes. Generic lifecycle traces are also context-insensitive; mail/application event kinds are service constants. Custom injected log handlers are not automatically protected. |
| JS 1 | Incomplete sanitizer: `ui/admin/src/collections/relation.ts` | `htmlText` is explicitly a display-summary helper, not an HTML sanitizer. Inspected `RecordCell`, `RelationPicker`, `fields.ts` consumers render its result as React text/attributes, with no HTML insertion sink. Encoded markup can remain literal text; future `innerHTML` use would invalidate this classification. |

No source suppression was added to lower the reported count. New full-scope scans of the integrated runtime remain necessary, including verification that the CodeQL scope guard still rejects diff-only output.

## Container inventory: remaining risk, not an empty gate

The old image SARIF still contains 26 `libc6 2.36-9+deb12u14` records and six `libssl3 3.0.22-1~deb12u2` records: 21 MEDIUM, nine LOW, two UNKNOWN, without a fixed version in that scan. The exact IDs remain in the recheck JSON and the earlier [individual inventory](evidence/runtime-data/image-inventory-review.json).

`deployments/Dockerfile` pins base digests, builds the main server with `CGO_ENABLED=0` and ships native PostgreSQL clients plus their library closure. Consequently, a pure-Go main binary does **not** remove libc/OpenSSL exposure from backup helpers. The agent reviewed the Dockerfile and source trust boundaries; it did not rebuild or dynamically prove the absence of every native library call.

The important previous classifications were independently checked against current primary records:

- The [OpenSSL 2026-11-10 advisory](https://openssl-library.org/news/secadv/20261110.txt) excludes 3.0 for CVE-2026-88822, -88823, -95691 and -95692. The [Debian record for -88822](https://security-tracker.debian.org/tracker/CVE-2026-88822) still marks the installed Bookworm version vulnerable. This confirms the source conflict; it does not authorize deleting all four inventory records.
- The same upstream advisory describes CVE-2026-107496 in CMS encryption and states that TLS/QUIC do not use that path. No CMS-encryption entry point was found in the reviewed PgBase code. Complete native-helper reachability was not established, so the record remains.
- The [CVE-2026-100290 Debian record](https://security-tracker.debian.org/tracker/CVE-2026-100290) describes glibc regex allocation-failure cleanup and memory-pressure crashes; [CVE-2026-115819](https://security-tracker.debian.org/tracker/CVE-2026-115819) describes a wide-scanf one-byte overwrite. Both remain unfixed for the installed Bookworm version. UNKNOWN severity is not evidence of harmlessness, and all-architecture descriptions preclude an amd64-only dismissal.
- [CVE-2026-97399](https://security-tracker.debian.org/tracker/CVE-2026-97399) is Power8-specific. Its applicability restriction is meaningful only for an actually verified architecture. This review does not carry an amd64 finding over to every released platform.

The remaining libc CVEs were inventoried and their prior dispositions read; their native function reachability was not independently exhausted. The prior OpenSSL PowerPC-specific LOW classification was not freshly revalidated after its upstream URL was unavailable. Current final-image inventory, dependency audit and SBOM gates belong to the integrated qualification, not this historical artifact recheck.

## Coverage ledger and explicit gaps

This table records concrete inspected source/call boundaries, rather than assigning a module-wide “audited” flag after one read. The scanner table above adds its listed sink/caller files. Focused regression execution applies only where a test result is explicitly recorded.

| Area | Reviewed files and boundary | Result / unfinished scope |
| --- | --- | --- |
| Administrator identity | `internal/adminauth/{service,password,password_recovery,email_otp,token}.go`; `internal/api/{admin_middleware,client_address}.go` | Password hashing/admission, recovery/OTP consumption, canonical session token, same-origin/CSRF and trusted proxy parsing inspected. No new bypass demonstrated. All administrator/TOTP concurrency schedules were not rerun by this agent. |
| User identity / session | `internal/userauth/{service,session_refresh,impersonation,oauth,oauth_http,oauth_oidc,oauth_apple,oauth_profiles}.go` | Login/register, refresh row-lock/CAS, impersonation-to-admin-session binding, OAuth state/link/nonce and ID-token fallback inspected. Generic OIDC defects corrected; Apple/provider-specific edge behavior and every auth collection rule remain incomplete coverage. |
| Avatar HTTP boundary | `internal/userauth/oauth_avatar.go`; URL, DNS validation, pinned dial and response decode | HTTPS/private-address guards, size/dimension limits and timeout inspected. Special-use addresses beyond `net.IP.IsPrivate` and redirect-hop policy remain further review; no reachable internal-service exploit was demonstrated. |
| Authorization / data access | `internal/api/{service_tokens,rules,file_handlers}.go`; portions of `internal/serviceauth/service.go`, `internal/filestore/{service,public,token_resolution}.go`; `internal/query/{compiler,codec,root_page}.go` | Admin ownership of policy/token changes, file token principal resolution and parameterized query construction inspected. Full policy-expression/compiler correctness and arbitrary multi-tenant policy combinations are not certified. |
| Local storage / backup | `internal/filestore/local.go`; `internal/backup/{archive,local_repository}.go`; `backup.go` canonical paths, tree walk, checksum and command helpers; manager upload/restore callers | Generated object-key paths, atomic/exclusive publication, fresh extraction, integrity verification and cleanup inspected. Actual archive regressions added. S3, malicious local co-writers, large restores, power-loss durability and every restore cutover path remain separate QA work. |
| Extension host policy | `pgbase/extension_host_policy.go`; HTTP origin/redirect checks, filesystem path resolution and process capability allowlist | Capability-scoped host access inspected. Extensions are trusted operator code running in the process, not an OS sandbox for hostile tenants. Concurrent local path replacement and every extension API were not exhaustively tested. |
| Mail / observability | `internal/mailer/smtp.go`; `internal/observability/logger.go`; relevant middleware/CLI/backup-alert and lifecycle callers | Header/DATA boundaries, TLS minimum/default verification and JSON/error redaction inspected. External providers and custom log handlers were not tested. |
| Integrity primitives | `internal/cursor/v5.go`, `internal/schema/operation_cursor.go`, `internal/recordartifact/artifact.go`, SQL console digest functions | MAC domains, full-width comparison, bound encodings and concrete hash writers inspected. Full SQL console execution parser/runtime was outside this agent's audit. |
| SDK / UI | Changed TS/Dart Realtime files and AuthStore callers; UI relation display helper and concrete React sinks | Independent changed-path review and static XSS sink review, not whole UI/browser compatibility certification. |
| Jobs / load evidence | Backend fencing diff and parent open-loop harness | Independent transaction/lease and measurement review. Whole job/realtime subsystem qualification remains with backend/root and final CI. |
| Supply chain | `deployments/Dockerfile`, checked-in SARIF ZIPs, prior runtime-data inventory; selected upstream/vendor advisories | All remaining scanner records retained. No new full-source scan, fresh container build or complete native-library exploitability proof by this agent yet. |

The `codex-security:deep-security-scan` skill was read, but the required deep-scan coordinator/completion server capabilities were absent from the session registry. No automated Deep Security Scan was started or represented as completed. Manual source review, local reproductions and real separate-agent review continued under the user's explicit engineering authorization.

## Compatibility and release conditions

Generic OIDC issuers must supply numeric `iat` and `exp`, honor `nbf`, publish RS256-compatible signature keys, and avoid malformed explicit optional key constraints. Providers relying on incomplete ID tokens, weak RSA keys or encryption-only JWK metadata will now fail authentication and need configuration/provider correction. No secret rotation or DB migration is required by these fixes. The explicit sixty-second future allowance should be considered when configuring system time synchronization.

The expanded archive limit includes tar framing as documented above; no API signature or DB migration is required. Failed generation/extraction must be handled as an error and never published as a complete backup.

All four security corrections and their scoped race/independent results are complete. **This report is not a Ready declaration for PgBase.** Final integrated-head CodeQL/container/dependency gates and whole-system operational/performance qualification still need their own evidence. Remaining native dependency risks and the coverage exclusions above must remain visible even if every automated gate passes.
