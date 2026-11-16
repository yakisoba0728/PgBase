# Final nonperformance security CI reconciliation

## Decision and exact source

The final-source Go, JavaScript/TypeScript, dependency and image security jobs
succeeded. Their artifacts preserve the previously classified **24 Go, one
JavaScript and 32 image findings**. The bounded comparison found no new finding
identity or newly established blocking vulnerability. The native-library
limitations below remain unresolved; a successful HIGH/CRITICAL gate does not
dispose of the other image records.

| Identity | Value |
| --- | --- |
| Published PR source | `56a881f47d0c27877b91959d7df641c2f603ef03` |
| Actual analyzed PR merge | `0a517baea58bf65e2293f15fb0a9fb54321f7eb7` |
| Source and merge tree, independently compared | `9b4a2e78540c3737f03f6fcc27398bfaa0424180` |
| Final nonperformance run | [43836410887](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887) |
| Historical comparison run | [43820007603](https://github.com/yakisoba0728/PgBase/actions/runs/43820007603), source `a1a652289f0bc817f18db257282c758e66b4a672` |

| Final job | Job ID | Observed conclusion |
| --- | --- | --- |
| Go CodeQL | [133194027639](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027639) | Success |
| JavaScript/TypeScript CodeQL | [133194027411](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027411) | Success |
| Dependency and image audit | [133194027112](https://github.com/yakisoba0728/PgBase/actions/runs/43836410887/job/133194027112) | Success |

The final job logs show checkout of the actual merge above. Both language
artifact summaries identify that merge and this workflow run. These observations
qualify the stated tree; a later report-only commit is not relabeled as the
scanned commit.

## Downloaded artifacts and integrity

Each ZIP was freshly downloaded for the final run and its SHA-256 matched the
GitHub artifact metadata. Both source summaries' embedded SARIF hashes were
also recomputed. Full digests, metadata, compressed decoded job logs and the
machine-readable comparison are retained in
[`evidence/phase-nonperformance-security-ci/`](evidence/phase-nonperformance-security-ci/README.md).

| Final artifact | Artifact ID | Bytes | SHA-256 |
| --- | --- | --- | --- |
| Go SARIF and summary | `14210922787` | 92,277 | `ef90d954308f2ab1f13f5d5a34a6cb005cbb713860579746dca39613925d9a91` |
| JS/TS SARIF and summary | `14210344766` | 122,430 | `ef740943f00d85a00ae87612c41cd06d7e33ae23b5d07ba73900698870b00e0a` |
| All-severity and HIGH/CRITICAL image SARIF | `14211495758` | 15,492 | `ace5ab0a04afdf534f9d147d8f3b14170bb9a3ee2b462ff84a4ae194e47d8482` |
| Runtime assets | `14210619448` | 206,513 | `40ab04aaa8cd0950d2f282500a1fd27e4d5e7f04b0e25b2296d7c1364c9966a9` |
| SBOM | `14211461144` | 115,260 | `aed283eb7f05596dd5fdb972073a695e6d2fe71c39c78db50659f1bcb74a3ae2` |

The included historical baseline ZIPs are copies of previously retained bytes,
checked against their recorded GitHub digests. They are explicitly marked as
historical copies, not fresh scans or fresh downloads.

## Source findings and analysis coverage

CodeQL **2.27.4** completed **37 Go and 105 JS/TS query evaluations**. Each job
also passed the SARIF evidence parser's 12 tests. Logs retain
`skip-queries: false` and no precomputed diff ranges; summaries report
`diffRangeRestricted: false`, no incremental-mode value and successful
invocations. SARIF rule-descriptor counts of 35 and 103 are separate from the
evaluated-query counts.

The final reports record **485 Go and 330 JS/TS extracted locations** and no
warning/error extraction notifications. All seven changed Go runtime paths are
present: object GC, restored-storage reset, staging, storage migration, variants,
OAuth avatar policy and the system-migration embed registry. Both changed TS/UI
runtime paths, `sdk/typescript/src/realtime.ts` and
`ui/admin/src/storage/BackupsView.tsx`, are also present.

Comparison resolves each finding's rule ID and primary source URI/region and
matches the rule, message and partial-fingerprint multiset, including duplicate
identities. All 25 identities match the baseline. The final Go report reordered
its findings and reindexed rule/artifact tables, so raw-record or code-flow
byte equivalence is not claimed. Resolved primary locations match except for
the migration-checksum call's known line shift from 297 to 300; JS primary
locations match. Of the **69 source files in reported dataflow paths**, only
`migrations/system/embed.go` has a changed Git blob against the baseline. Its
change registers migration 69; the flagged hash still identifies public
migration SQL and does not store passwords. All production/runtime files are
unchanged from the immediately preceding scanned source `30523dda`; source
`56a881f4` changes only the doctor integration test described below.

The existing per-location reasoning in
[`final-security.md`](final-security.md#independent-classification-of-all-24-go--one-js-locations)
therefore remains applicable within its recorded limits: concrete digest
writers, validated allocation inputs, archive path checks, structured mail and
logging, non-password hashes, and React text consumers. This follow-up did not
reclassify the raw SARIF as zero findings or add suppressions. The raw summaries
retain their scanner-produced `unreviewed` label; this document records the
bounded human reconciliation separately.

Coverage is the configured Linux Go build and JS/TS analysis. It does not
establish CodeQL coverage for every Windows/BSD implementation, Go test fixture,
Dart source, SQL migration, policy expression or external provider. Test/race
and platform-build results remain separate evidence.

## Dependency/image findings and retained limitations

The audit logs report `No vulnerabilities found.` for `govulncheck ./...` and
zero vulnerabilities for the recorded npm audits. The final image is
`sha256:21b6bdde2b92e82c72a8fc0c33ab1d960d6df3ad89be6773775c80e88e91cb30`.

The all-severity image SARIF contains **32 records: 21 MEDIUM, nine LOW and two
UNKNOWN**. Every result record and every rule description equals the historical
baseline after JSON parsing. The separate HIGH/CRITICAL SARIF contains **zero**
results. The Trivy allowlist remains empty; no exception was added.

The previously recorded dispositions remain visible:

| Records | Retained disposition |
| --- | --- |
| 25 | libc native-helper risks whose complete reachability has not been disproved |
| 4 | OpenSSL upstream/distribution applicability conflicts; records remain retained |
| 1 | Affected CMS-library record without a demonstrated reviewed application entry point |
| 2 | Architecture-specific limitations applicable to this verified amd64 image only |

These dispositions come from the existing bounded classification in
[`final-security.md`](final-security.md#all-32-image-records-retained), not a new
repository-wide advisory audit. The two UNKNOWN native records remain
unresolved. A statically linked Go main binary does not eliminate exposure in
the shipped PostgreSQL helper binaries. No complete native-call-path or exploit
absence claim is made.

Fresh package metadata and SBOM URLs agree on amd64 and retain
`libc6 2.36-9+deb12u14` and `libssl3 3.0.22-1~deb12u2`. All 12 captured package
versions agree with the runtime manifest and SBOM. The SBOM contains 62 packages,
972 files and 1,135 relationships; these are inventory counts, not proof that
every file or dependency path is safe.

The copied trust bundle contains 148 unique certificates. Independently
recomputed DER fingerprints and the count match its manifest; bundle SHA-256 is
`e9c7b63ee2623386e9b0a3ee32f499d01a8ea23dc83b8383ddff9f8e749543b7`.
The image records `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`. The copied
timezone source begins `# version 2026f` and hashes to
`1708f2c388c5b602defbc56eba3237a9615f8cd28dbbf2f58d7534ff434af5a6`.
These bytes match the previously reviewed data. This check did not repeat TLS
handshakes, certificate-validity evaluation or compiled-timezone behavior in
the image; the earlier bundle-validity limitations remain.

## Earlier attempts and the finite corrections

Run [43833754019](https://github.com/yakisoba0728/PgBase/actions/runs/43833754019)
analyzed source `d6cc73b8860dcca0b2bf175b9dfc2baee0c31c3f`, merge
`8888312b8a650be0d654073122f553128e528d1c`, shared tree
`d7fe14f674f4fc7d402739cf8c99610afc95464a`. Its three security jobs succeeded and
produced the same finding identities, but that run's separate static and browser
build failures remain historical failures. Its artifacts are retained under
`ci-43833754019/` and are not substituted for the final-source scan.

The static correction removed production-unused `queueUnreferencedVariant`
instead of suppressing the diagnostic. Its two regressions now exercise
durable pre-Put intent, `DrainOne` and the actual object-reference lock:
pending publication preserves bytes, COMMIT retires obsolete intent while
preserving referenced bytes, and delayed ROLLBACK retains then drains orphan
intent. Independent PostgreSQL **17.9 / UTF8 / race** execution passed exactly
these two tests, with zero failures/skips and package elapsed time 3.108 s.
Author commit `e38e6d9854539ec2ff0859707109ecda5e3c670f`, independently tested
commit `122f86d261ef0f486d0a67c0f029aa92c27f383b` and previously published source
`30523ddaf27f8ffebd8b59c5ef802186107a408e` share tree
`7a28c45d5d5bb46156140953332422c8cb13bbf3`. Those runtime and test files remain
unchanged in the final source. The logs and reproduction command are in
[`static-correction/provenance.json`](evidence/phase-nonperformance-security-ci/static-correction/provenance.json).


Run [43834531061](https://github.com/yakisoba0728/PgBase/actions/runs/43834531061)
analyzed that preceding source using merge
`495ea346467f5d90fc9d250531c9c8718233b6d3`. Its three security jobs succeeded and
its artifacts remain under `ci-43834531061/`. Its separate PostgreSQL 16 doctor
integration test failed because the fixture still asserted latest schema 68.

The final correction changes only `internal/doctor/checks_integration_test.go`:
expect schema 69, preserve actual schema-67 rejection, add schema-68 rejection,
and retain latest acceptance and dirty-checksum cases. Independent PostgreSQL
17.9 / UTF8 / race execution passed **one top-level test and all 12 subtests**,
zero failures/skips, in 3.872 s. Tested source
`bedac6a9f3da74886e5155bc2eb005e915a0bcd6` and published source `56a881f4` share
the final tree in the identity table. A narrow search found no additional stale
latest-68 expectation; three deliberate historical migration/restore boundaries
remain unchanged. The independent evidence is in
[`phase-nonperformance-doctor-qa/provenance.json`](evidence/phase-nonperformance-doctor-qa/provenance.json),
and final source/merge/tested-tree correspondence is recorded in
[`source-provenance.json`](evidence/phase-nonperformance-security-ci/ci-43836410887/source-provenance.json).
No local tests were repeated for this final artifact rebinding.

Initial browser job `133185152151` downloaded 12,213 bytes for zlib and then
failed the pinned checksum. The response body was not observed, so its cause is
not asserted. The same-source security job `133185153037` downloaded the archive
and logged `zlib.tar.gz: OK`; the final security job did so again at
`2026-11-16T09:05:12.0499500Z`. A bounded direct check of the
[official zlib archive](https://zlib.net/zlib-1.4.tar.gz) returned HTTP 200,
1,507,140 gzip bytes and the original pinned SHA-256
`02dd159783677a749c04adbdd02b55b8a2bcc7e054f51b76b549199a916ad584`, also advertised
by the [official zlib page](https://zlib.net/). No source URL or checksum change
was warranted or made. Download metadata and independently read failure
excerpts are retained; browser execution outcomes belong in the SDK/UI and
coordinator reports.

## Remaining decision boundary

This follow-up inspected the supplied security jobs/artifacts and existing
finding classifications. It did not start a new repository audit, provider
integration, performance investigation, benchmark or sustained load. It does
not decide the other final qualification gates. The historical 547 dropped
requests remain unresolved, and the overall release decision remains
**Not Ready**. Native-helper uncertainty and source/platform exclusions remain
visible even when the configured automated security jobs succeed.
