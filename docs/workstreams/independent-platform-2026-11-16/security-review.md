# Source security review and corrected CodeQL evidence

This continuation independently checked the actual artifacts and code behind [run 43751625628](https://github.com/yakisoba0728/PgBase/actions/runs/43751625628). Its source was `cf50eed36e7d2f15915ade9f66a156faf34871ab`; the relevant runtime files were unchanged at the handoff `6dd8f6449f7b6bc1a986ff7444b898776abf1c7b`. Review findings below belong to that baseline. A green analysis job means that analysis completed, not that the source has no vulnerabilities.

## What the original evidence actually says

| Measure | Go | JavaScript/TypeScript |
| --- | ---: | ---: |
| Job | 132926640587 | 132926640381 |
| SARIF results / printed findings | **27** | **3** |
| Error / warning level after resolving rule metadata | 15 / 12 | 2 / 1 |
| Queries loaded in the execution log | **37** | **105** |
| SARIF rule descriptors, including metric rules | 35 | 103 |
| Successfully extracted file URIs | 483 | 323 |
| Original artifact ID | 14176194587 | 14176811964 |

The previous zero-result report, different query counts and artifact identifiers were incorrect. The job logs and SARIF agree on 27 and 3 results. There is no evidence establishing why the earlier prose substituted different numbers. Counts of loaded queries, rule descriptors and extracted source files describe different things and must not be substituted for each other.

The original reporter looked only in `tool.driver.rules`, which is empty in both reports. CodeQL placed descriptors in `tool.extensions`, referenced by each result's `rule.toolComponent`. That defect caused **null severity metadata**, not zero results. `scripts/summarize-codeql.py` now resolves the actual component, rejects conflicting/invalid references and incomplete analysis, preserves every finding and fingerprint, and records source/run binding, report digests and an explicit `unreviewed` classification. Its regression suite includes an independently suggested conflicting-index case that failed before the correction.

Original ZIP bytes are committed in [evidence/codeql-original](evidence/codeql-original), together with a manifest and exact log excerpts. Their SHA-256 values were checked against the GitHub artifact API:

- Go: `d325c391955a94a1646d3055046e40d277738f0c9b433be48c083054c89a5092`
- JavaScript/TypeScript: `a77a9ff60abfd21cc128dfdae50fb208b36116e4a553a7c0d594e7b0b10a188a`

The ZIPs deliberately preserve the original `findings.json`, including its defective metadata. The separate [classification JSON](evidence/codeql-classification.json) contains corrected metadata, original fingerprints, all 30 ordinal identifiers, rationale and resolution. It does not silently edit the original scanner evidence.

## Classification of every original result

The count is **27 false-positive exploit claims on the inspected concrete paths, one trusted-configuration risk that was defensively refactored, and two findings identifying one real example destination-trust flaw**. “False positive” applies to the supplied trace and claim. It is not a statement that the module has no other defects.

| Original result | Location / claim | Finding and concrete boundary |
| --- | --- | --- |
| Go 1 | `internal/mailer/layout.go`: HTML escape bypass | Administrator-authored HTML is trusted markup. Dynamic URL interpolation could nevertheless corrupt a link and insert extra nodes in a preview. Contextual rendering now separates dynamic strings from markup. Preview is admin-only and the UI separately sanitizes nodes; no unauthenticated or cross-user admin XSS was established. |
| Go 2–6 | `compression.go`, `custom_routes.go`, `middleware.go`, `schema_handlers.go`, `observability/metrics.go`: reflected XSS | The scanner substitutes HTTP response-writer implementations for the writer passed to digest helpers. Actual `writeDigestField` / `writeDigestPart` callers supply SHA-256/HMAC writers. The concrete invocation cannot reach those HTTP sinks. |
| Go 7 | `internal/mailer/smtp.go`: email injection | Header fields reject CR/LF and mailboxes are parsed; attachment headers reject CR/LF. The standard SMTP DATA writer dot-stuffs body terminators. Added a local SMTP fixture regression for both boundaries. Intended body text is not header injection. |
| Go 8 | `internal/query/root_page.go`: allocation overflow | Normalized page limit is at most 200; `batchLimit+1` is at most 201. |
| Go 9 | `pgbase/extension_objects.go`: allocation overflow | Explicit `1 <= limit <= 1000` check precedes SQL and allocation, so `limit+1 <= 1001`. |
| Go 10 | `internal/cursor/codec.go`: allocation overflow | Already allocated JSON payload plus a fixed 32-byte signature, bounded to 4096 decoded bytes. No reachable integer overflow from supported inputs was established. A subtraction-form remaining-budget check now makes the invariant explicit before hashing/allocation. |
| Go 11–12 | `internal/query/compiler.go`: allocation overflow | Capacity comes from the length of an existing field-validated map, whose resident entry storage prevents MaxInt-scale counts. There is no unchecked user numeric allocation size here. |
| Go 13 | `internal/schema/operation_cursor.go`: allocation overflow | Fixed-shape payload contains canonical UUIDs and timestamps with bounded years, plus a fixed signature. |
| Go 14–15 | `schema_document.go`, `schema_policy_checksum.go`: allocation overflow | Counts come from existing substantial struct slices/maps with bounded import inputs. Their resident representations cannot approach the integer overflow threshold. This does not promise unlimited schema-import resource cost. |
| Go 16 | `internal/backup/archive.go`: Zip Slip | Canonical relative paths, rejection of absolute/backslash/parent paths, regular-file-only entries, a new private directory, exclusive file creation and manifest verification defeat the supplied traversal claim. A **separate cumulative size overflow** was found and fixed below. |
| Go 17 | `internal/adminauth/mfa.go`: narrowing conversion | Existing six-byte length bound already prevents int32 overflow. A separate noncanonical signed OTP alias was reproduced and fixed below. |
| Go 18 | `internal/cursor/v5.go`: narrowing conversion | Both the high and low 32-bit halves of the int64 version are compared and ANDed. A high-bit-only mismatch regression confirms the full-width check. |
| Go 19 | `internal/adminauth/service.go`: weak password hashing | SHA-256 normalizes existing encoded Argon2id hashes/setup tokens for equal-width constant-time comparison. Actual password storage uses salted Argon2id (64 MiB, time 3, parallelism 4). |
| Go 20 | `migrations/system/embed.go`: weak password hashing | SHA-256 covers embedded migration SQL named `adminPasswordResetsSQL`; it is a source checksum, not a user password. |
| Go 21–23 | Three Go example startup log paths | Supplied traces conflate request hook events with distinct startup hook events having fixed kinds. Request errors do not propagate into these startup `log.Fatal` calls. Custom operator/hook errors are not guaranteed single-line; this is not an endorsement of arbitrary text log sinks. |
| Go 24 | `internal/api/middleware.go`: log injection | Request IDs use an ASCII allowlist; rejected values are replaced. The sink uses structured logging. |
| Go 25–27 | CLI database/run and backup-alert logging | Shipped CLI sinks use redacting JSON `slog`; embedded default discards via JSON handler. Fields are not concatenated into raw log records. Custom handlers remain the embedding application's boundary. |
| JS 1 | `relation.ts`: incomplete HTML stripping | Current callers use the result as React text children/attributes. The helper is a text summary, not an HTML sanitizer; five actual renderer regressions and a contract comment now enforce that distinction. |
| JS 2–3 | JavaScript example login/SSE destination | **Confirmed shared root cause:** a crafted `?api=` link selected where subsequently entered credentials were sent. Removed query-selected origins; deployment-owned configuration replaces them. The user must enter credentials in the crafted example. The SSE site is not separate proof of stealing a legitimate-server-issued token because login also used the selected destination. |

## Additional defects reproduced and corrected

### Archive expanded-size arithmetic

`Unpack` added each tar entry size to the total before checking the 8 GiB expanded-size budget. A small first entry followed by a `MaxInt64` size header wrapped that total negative. A tiny truncated archive reproduced reading past the intended budget check: the unchanged implementation returned `unexpected EOF` instead of rejecting the oversized header. The fix checks the entry against the remaining budget before addition. The regression verifies early rejection and staging cleanup without allocating a huge body or consuming storage.

The affected restore paths require an administrator session or setup authorization. This is a lower-trust archive-input boundary, not an unauthenticated public upload vulnerability. Existing traversal, integrity and restore behavior remain required.

### TOTP canonical format

At a deterministic timestamp, both `070624` and the signed alias `+70624` were accepted by the old `Atoi` parser. Exactly six ASCII digits are now required. This repairs the input contract; the test does not demonstrate a second-factor bypass, fewer guesses or recovery of a secret.

### Mail template contextual rendering

The old code interpolated strings into trusted HTML and then converted the result to `template.HTML`. A valid HTTPS action URL containing quotes and HTML syntax reproduced extra nodes and a damaged link. `RenderHTML` now parses trusted administrator markup and supplies existing placeholders as string-valued functions, allowing `html/template` to escape values in their actual contexts. Preview, action, OTP and alert paths use the same renderer. Unsafe dynamic URL schemes become `#ZgotmplZ`; malformed or ambiguous HTML contexts return an error without partial output. Save validation rejects malformed contexts.

Administrator templates remain trusted markup, not sanitized user content. Previously persisted malformed or ambiguous templates can now fail delivery and require repair/re-save. See [architecture and migration](architecture-and-migration.md).

### SDK authentication lifetimes

Both SDKs had paths returning a former user's response after a shared `AuthStore` changed. File preparation could also finish after a switch and dispatch the earlier user's selected file with the new bearer. Generation scopes now cover response/error decoding and asynchronous file preparation. Independent review additionally exercised concurrent login adoption and delayed Dart persistence; the final fixes and exact test totals are recorded in [validation.md](validation.md).

This protects SDK operation completion. Data already delivered to application code cannot be revoked; applications must clear identity-bound caches and subscriptions on authentication changes. A rejected write may already have committed at the server, so automatic mutation retries are unsafe.

## Review and evidence limits

### A second evidence defect: PR-only CodeQL reporting

The first combined runtime's [run 43777701630](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630) produced zero Go and one JavaScript finding. Those numbers were **not accepted as a whole-source result**. Raw SARIF declared `incrementalMode: diff-informed` and included `codeql-action/pr-diff-range`; logs computed 391 changed ranges in 148 files and invoked the corresponding extension pack. The extractor still covered 483 Go/327 JavaScript files. Broad extraction therefore did not prove broad alert reporting.

The reusable analysis inherited the PR event and the pinned CodeQL action enabled diff-informed queries by default. Job-level `CODEQL_ACTION_DIFF_INFORMED_QUERIES=false` now reaches initialization. `summarize-codeql.py --require-full-scope` rejects any incremental mode or PR diff-range extension **after preserving findings and summary**, with three new regression cases. All 12 parser tests pass; independent security review repeated them and rejected both actual limited reports. Original ZIPs remain in [codeql-diff-only](evidence/codeql-diff-only). The final unrestricted rerun is recorded in [validation.md](validation.md), separately from this rejected evidence.

The corrected [run 43780150632](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632), on runtime `5e4b71d1`, reports **24 Go and one JavaScript/TypeScript result**. Both summaries have null incremental mode and `diffRangeRestricted: false`; raw SARIF and CLI logs independently agree. The loaded queries remain 37/105, extracted file counts 483/327, and rule descriptors 35/103. All remaining records match the earlier narrowly classified false-positive claims. Matching includes source URI because three startup-log sites share fingerprints. Go 1/10/17 and JS 2/3 are no longer reported; the concrete code/test review, not that disappearance, supports the fixes and distinctions. Unchanged ZIPs, log excerpts and hashes are in [final qualification evidence](evidence/qualification-final/README.md).

### Runtime image and dependency inventory

The first runtime image's HIGH/CRITICAL gate contained zero findings, but its all-severity report contained **37 records: 21 MEDIUM, 9 LOW, seven UNKNOWN**. All 37 are individually retained in [image-inventory-review.json](evidence/runtime-data/image-inventory-review.json). Package metadata, concrete library/data paths and upstream applicability were reviewed separately.

| Package | Original records | Action / remaining boundary |
| --- | ---: | --- |
| ca-certificates | 1 | Build the public bundle from `20250419~deb12u2`, pinned to a package hash verified against Debian's signed security index. The generated bundle contains 148 roots and excludes the two roots removed by [DLA-4845-1](https://lists.debian.org/debian-lts-announce/2026/10/msg00039.html). |
| tzdata | 3 | Pin `2026f-0+deb12u1`, export all zone files and matching metadata. Actual Go zone-loading checks reproduced corrected offsets; job scheduling uses `time.LoadLocation`. See [Debian package record](https://tracker.debian.org/news/1817009/accepted-tzdata-2026f-0deb12u1-source-into-oldstable-security/). |
| golang.org/x/crypto | 1 | Upgrade 0.58 to 0.59 with official tag/checksum verification. The affected obsolete OpenPGP code was not in PgBase's production graph before or after; this is dependency hygiene, not a proven reachable-vulnerability repair. |
| libc6 | 26 | Retain native-helper dependency risk. The main Go server is CGO-disabled, but pg_dump/pg_restore load libc. No fixed Bookworm version was reported by this scan, and reachability of every flagged function was not proven absent. |
| libssl3 | 6 | Retain individual applicability decisions and source conflicts. No blanket suppression or false-positive classification was added. |

The pinned distroless **base** has 150 roots; only AAA Certificate Services and emSign ECC Root CA - G3 are removed by the new 148-root bundle. That is not a measured comparison with the old final image: its Dockerfile overwrote the bundle from the Go builder, whose actual bundle bytes were not extracted. The candidate's content is directly checked. Package labels alone were not treated as proof of trust contents.

A separate native-client path defect was verified. The exact copied OpenSSL 3.0.22 library defaults to `/usr/lib/ssl/cert.pem` and `/usr/lib/ssl/certs`, which the pinned base and subsequent extraction stages do not provide. Its default-path probe loaded zero certificates with those absent paths; an explicit generated bundle loaded all 148. Final `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt` now reaches native backup helpers and Go. It does not change explicit application trust overrides or disable TLS verification.

Data generation verifies package SHA-256, name, version and `Architecture: all` before extraction. Debian's packaged helper receives isolated configuration, root source, local-certificate and hook paths; build-host trust is not imported. Matching package status/copyright is copied with the data, without replacing the existing PostgreSQL native-library closure. [Signed provenance and local probes](evidence/runtime-data/README.md) retain the exact evidence. The CI runtime inventory captures actual final certificate fingerprints, trust-path environment, timezone source and package versions without running a shell/application in the image or following symlinks into the host.

Four OpenSSL findings have a source conflict: [upstream's 2026-11-10 advisory](https://openssl-library.org/news/secadv/20261110.txt) excludes OpenSSL 3.0 for CVE-2026-88822, -88823, -95691 and -95692, while Debian marks the installed package. The CMS issue is a library vulnerability, although TLS/QUIC do not invoke the affected CMS encryption function and no such call was identified in reviewed PgBase/libpq paths. Architecture-specific exclusions apply only to the actual amd64 image. Remaining inventory is visible rather than waived by a successful gate.

The final built image's captured bundle matches the locally verified SHA-256 and 148-root inventory, both removed fingerprints are absent, the configured native trust file is correct, and its actual timezone source and package metadata agree on 2026f. Its all-severity report contains **32 remaining entries: 21 MEDIUM, nine LOW and two UNKNOWN**, all in libc6/libssl3. The five removed records correspond to the data/module updates above; zero new image records were introduced in this comparison. HIGH/CRITICAL findings remain zero. [Final artifact bytes and binding](evidence/qualification-final/README.md) preserve the actual final-image evidence.

Three actual agents worked in separate Git worktrees: security reviewed 18 Go findings, backend/performance reviewed the other nine, and frontend/SDK reviewed all three JavaScript findings. Root verified artifact bytes/counts, integrated changes and ran focused regressions. Security and performance reviewed each other's runtime changes; security's final SDK review found additional races that were sent back for failing tests and fixes. These are real independent reviews, not parallel CI jobs presented as reviewers.

The complete affected-package local security integration run passed 237 top-level tests (409 passing events including subtests). Four top-level S3 tests/six S3 events were excluded by existing unconfigured-fixture skips; no skip was added or weakened. S3, browser and multi-platform claims require their separate CI results. External SMTP/OAuth providers, arbitrary custom extensions/log handlers, all possible tenant policies and exhaustive whole-repository exploit analysis remain outside this scoped evidence.
