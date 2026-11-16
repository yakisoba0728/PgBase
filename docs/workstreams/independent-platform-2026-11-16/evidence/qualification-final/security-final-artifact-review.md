# Independent final CodeQL and runtime-image artifact review

## Scope and provenance

Read-only Security subagent review of run **43780150632**, published head `5e4b71d1f602c9c136809bf65f2375bf75fc4a9f`, synthetic merge `2d712ba6239f2d19f1329400848173842ed7874c`.

Independently fetched the GitHub artifact API and verified all six retained ZIP SHA-256 digests and byte sizes (Go, JS, runtime-assets, image SARIF, SBOM and mixed HTTP), then compared every extracted file byte-for-byte with its ZIP member. Mixed HTTP content was integrity-checked here, not re-reviewed as a new performance measurement.

Independently fetched the GitHub Git commit object for the synthetic merge. Its tree `4599bc705d4c4ec440e273cc6db3397f3edcc11d` equals the inspected local integrated tree. Its parents are the stated main baseline and published head. This binds the source re-review to the candidate whose artifacts were inspected.

Full machine evidence: `security-final-artifact-review.json`. Raw API digest metadata: `final-artifact-api-independent.json`. No repository files were modified, no raw scanner classifications were rewritten and no runtime/browser tests were repeated by this artifact review.

## Final CodeQL: full configured scope, not zero findings

| Measure | Go | JavaScript/TypeScript |
| --- | ---: | ---: |
| Actual results | 24 | 1 |
| Error / warning | 14 / 10 | 0 / 1 |
| Successfully extracted source URIs | 483 | 327 |
| Rule descriptors | 35 | 103 |
| SARIF `incrementalMode` property | Absent | Absent |
| PR diff-range extension | Absent | Absent |
| `executionSuccessful` | true | true |

Both use CodeQL 2.27.4. This review does not substitute descriptor counts for executed-query counts. The raw properties/configuration retain the configured security-extended query suite. The final artifacts do not have the prior diff-informed restriction.

I independently resolved each raw result's referenced extension rule and compared every field in the normalized findings JSON: rule/component, level, security severity, message, locations and partial fingerprints match exactly. The summary's counts, scope flags, extraction counts and report hashes also match raw SARIF. Its `classification: unreviewed` remains untouched; this report supplies the separate human/agent review.

### Every final result reconciled

All25 final results correspond to original supplied claims. The match key includes **rule + primary URI + complete partial fingerprints**: the three example startup-log sites share fingerprints, so omitting the URI would incorrectly collapse them.

| Final ordinal | Original ID | Current primary location | Decision |
| --- | --- | --- | --- |
| go 1 | go-002 | `internal/api/compression.go:176` | False positive for supplied concrete claim |
| go 2 | go-003 | `internal/api/custom_routes.go:228` | False positive for supplied concrete claim |
| go 3 | go-004 | `internal/api/middleware.go:266` | False positive for supplied concrete claim |
| go 4 | go-005 | `internal/api/schema_handlers.go:652` | False positive for supplied concrete claim |
| go 5 | go-006 | `internal/observability/metrics.go:468` | False positive for supplied concrete claim |
| go 6 | go-007 | `internal/mailer/smtp.go:146` | False positive for supplied concrete claim |
| go 7 | go-008 | `internal/query/root_page.go:205` | False positive for supplied concrete claim |
| go 8 | go-009 | `pgbase/extension_objects.go:482` | False positive for supplied concrete claim |
| go 9 | go-011 | `internal/query/compiler.go:94` | False positive for supplied concrete claim |
| go 10 | go-012 | `internal/query/compiler.go:95` | False positive for supplied concrete claim |
| go 11 | go-013 | `internal/schema/operation_cursor.go:75` | False positive for supplied concrete claim |
| go 12 | go-014 | `internal/schema/schema_document.go:320` | False positive for supplied concrete claim |
| go 13 | go-015 | `internal/schema/schema_policy_checksum.go:21` | False positive for supplied concrete claim |
| go 14 | go-016 | `internal/backup/archive.go:93` | False positive for supplied concrete claim |
| go 15 | go-018 | `internal/cursor/v5.go:199` | False positive for supplied concrete claim |
| go 16 | go-019 | `internal/adminauth/service.go:649` | False positive for supplied concrete claim |
| go 17 | go-020 | `migrations/system/embed.go:294` | False positive for supplied concrete claim |
| go 18 | go-021 | `examples/custom-routes/main.go:37` | False positive for supplied concrete claim |
| go 19 | go-022 | `examples/jobs/main.go:39` | False positive for supplied concrete claim |
| go 20 | go-023 | `examples/lifecycle/main.go:43` | False positive for supplied concrete claim |
| go 21 | go-024 | `internal/api/middleware.go:145` | False positive for supplied concrete claim |
| go 22 | go-025 | `internal/cli/database.go:84` | False positive for supplied concrete claim |
| go 23 | go-026 | `internal/cli/run.go:259` | False positive for supplied concrete claim |
| go 24 | go-027 | `pgbase/backup_alert.go:92` | False positive for supplied concrete claim |
| js 1 | js-001 | `ui/admin/src/collections/relation.ts:58` | False positive for supplied concrete claim |

Current boundary review retained the original rationale for each result. Hash helper callers still supply HMAC/SHA-256 writers, not the substituted HTTP writer sinks. SMTP header validation and DATA dot-stuffing remain. Allocation counts are bounded page/object limits, fixed-shape cursor data or lengths of already resident substantial maps/slices; no new unchecked numeric size was introduced. Cursor equality compares both int64 halves. Password paths use encoded Argon2id comparison or migration-source checksums. Log cases retain the separate startup/request allocations and actual JSON sinks. React editor-summary results still enter text children/attributes, not raw HTML. The code and caller paths relevant to these boundaries were re-read or checked for unchanged Git blobs.

The only changed retained sink files are the root-page cache addition (no sequential page-limit change), archive arithmetic correction (path controls unchanged), v5 encoding budget correction (full-width Matches unchanged), and the relation helper's explicit text-only contract comment. The JSON records exact original/current blob IDs and full per-result rationale.

Exactly these original results are absent, with no unmatched/new final result:

- **go-001**: `template.HTML` conversion replaced by contextual rendering of trusted markup with string-valued data.
- **go-010**: cursor envelope check moved to a subtraction-form remaining budget before hashing/allocation. The original practical overflow claim was not established; absence is not reclassified as a proven exploitable bug fix.
- **go-017**: six ASCII-digit parsing removes the narrowing-conversion site and signed alias. Existing length bounds already defeated the original int32-overflow claim.
- **js-002 / js-003**: one deployment-origin trust flaw repaired at login and SSE sites by removing query-selected API origins.

Scanner disappearance alone is not proof of security. These changes have separate source/reproduction/regression evidence. All residual verdicts remain scoped to these claims and callers, not a clean-module or whole-platform guarantee.

## Final runtime data: actual built bytes verified

Actual image ID, identical in both image SARIF reports and runtime capture:

`sha256:2966e2bfca8988c3d3c87808f19ae2869279a32de07d08bd4a086f0a7106e8ba`

- Captured bundle SHA-256: `e9c7b63ee2623386e9b0a3ee32f499d01a8ea23dc83b8383ddff9f8e749543b7`.
- Reparsed all PEMs independently: **148 unique DER certificate fingerprints**, exactly matching the manifest.
- Actual captured bundle is byte-identical to the locally generated bundle from signed/hash-verified Debian packages.
- Both removed root fingerprints are absent: AAA Certificate Services `d7a7a0fb5d7e2731d771e9484ebcdef71d5f0c3e0a2948782bc83ee0ea699ef4`; emSign ECC Root CA - G3 `86a1ecba089c4a8d3bbe2734c612ba341d813e043cf9e8a862cd5c57a36bbe6b`.
- Manifest reports no certificate symlinks and the capture has only the expected bundle file.
- Actual image environment contains `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`.
- Captured `tzdata.zi` begins `# version 2026f`; SHA-256 `1708f2c388c5b602defbc56eba3237a9615f8cd28dbbf2f58d7534ff434af5a6`, byte-identical to the signed package payload.
- Independently matched SHA-256 for **all900 SBOM-listed zone files** against the generated signed-package data.
- Actual CA/tzdata package status and `.md5sums` bytes match the local generated metadata. Captured status and SBOM agree: CA `20250419~deb12u2`, timezone `2026f-0+deb12u1`, libc `2.36-9+deb12u14`, libssl `3.0.22-1~deb12u2`; SBOM identifies x/crypto `v0.59.0`.

The SBOM container record uses name `pgbase`, version/tag `audit-43780150632-1`, and separately reports container checksum `70342f64957c0bf71e9027a32514a0f31f7399969f7842dcbba07bf9843b54a6`. That value is retained as reported and is not relabeled as the Docker config image ID above. Workflow source shows SBOM, runtime capture and SARIF all consume the same image-ref without an intervening rebuild.

The old150/new148 comparison remains specifically about the pinned original distroless base, not an unobserved old assembled image. The final candidate's actual bundle now has direct container-capture evidence.

## Actual final image inventory: 32 retained records

HIGH/CRITICAL report: **0**. All-severity report: **32 = 21 MEDIUM + 9 LOW + 2 UNKNOWN**, or SARIF21 warning /11 note. Package distribution: **26 libc6 +6 libssl3**.

Every remaining package/advisory/version/severity/fixed-version/SARIF-level tuple exactly matches an original record. No new image finding appeared. All32 retain the prior bounded applicability decision in the machine JSON.

Exactly these five previous records disappeared:

| Package | Record | Candidate evidence |
| --- | --- | --- |
| ca-certificates | DLA-4845-1 | Actual148-root bundle and matching updated package metadata |
| tzdata | DLA-4792-1 | Actual2026f source/data and package metadata |
| tzdata | DLA-4854-1 | Actual2026f source/data and package metadata |
| tzdata | DLA-4867-1 | Actual2026f source/data and package metadata |
| golang.org/x/crypto | GO-2026-5932 | Actual module inventory v0.59.0; affected OpenPGP packages were not linked before upgrade |

The remaining inventory is not suppressed or declared universally false positive. Four OpenSSL reports retain the documented upstream-vs-Debian applicability conflict. The CMS report remains a library issue without a reviewed TLS/libpq CMS call path. Two PowerPC/POWER-specific reports are inapplicable to this amd64 artifact only. The other25 libc records remain native-helper dependency risk without exhaustive reachability clearance. No fixed version is supplied for any of the32 retained records.

## Conclusion and limits

The final source artifacts satisfy the required non-incremental configured-source evidence check, all25 source findings are individually reconciled and narrowly classified, and the image contains the intended authenticated trust/timezone data. This independently addresses the previous misleading zero-findings and stale-package-vs-file-content ambiguities.

This review does not replace the parent's full CI job verification, browser/recovery evidence or broader outstanding architecture/production qualification. No clean-platform, universal absence-of-vulnerabilities or production-readiness certification is made.
