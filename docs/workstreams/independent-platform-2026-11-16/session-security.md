# Subagent-first security continuation

## Runtime and concrete correction

Security agent `/root/security` started from published `d77fd45d` in an isolated
worktree. The final runtime of this slice is
`86dbfa822ea69e0c98867a66e52efdc634377c3a`, tree
`9a76dd6550d8b91a0a527ad82edea4d3aec05272`. No schema change is needed.

The inherited, uncommitted HTTP regression initially omitted the required
`registrationEnabled` property when updating a collection. Its first failures
were fixture errors, not vulnerability evidence. After correcting that fixture,
the unchanged auth-rule runtime returned **HTTP 200 instead of 401** for both
anonymous and authenticated record reads configured with
`@request.method != 'GET'`. Header/query negative predicates likewise admitted
the blocked actual request values. Positive predicates incorrectly denied valid
requests. Literal empty/false controls behaved as expected.

The HTTP adapter now carries the originating method, normalized headers, query
and request-ID context into auth services. The auth-rule resolver copies only
those transport fields; it still constructs body/presence/change values from
operation input and reloads the active actor from PostgreSQL. Request-less
embedded calls fail closed for HTTP-dependent rules, including nested
alternatives, function arguments and right-side operands. They retain body-only
and actor-only rules. The contract and upgrade behavior are documented in
[`docs/auth-rule-request-context.md`](../../auth-rule-request-context.md).

This is a conditional authorization bypass under configured request-dependent
auth collection policies, including the public record-read boundary. The test
uses disposable accounts and databases; no production access, external target
or credential was involved. It is not a claim that arbitrary users could change
collection policy, bypass every rule, or retrieve password hashes.

## Recovered changes and executed verification

Three previously unposted logical changes were independently inspected and
recovered, preserving the prior implementers' boundaries:

| Current local commit | Recovered change | Fresh verification |
| --- | --- | --- |
| `11f716cd` from `a52d57c2` | Explicit OAuth avatar ten-request redirect cap | Full userauth race suite includes long/short redirect controls |
| `cd6491db` from `28a0ae8b` | Standalone protected-file request metadata and storage-controller range lease | PostgreSQL HTTP owner/header/actor tests and range-body lease test |
| `ee5b5886` from `64b1940f` | Bounded failed-upload and variant cleanup | Slow deletion, exhausted pool, blocked publisher and later-commit preservation |
| `86dbfa82` | Auth-rule HTTP metadata correction | Anonymous/authenticated list/view; password login; request-less calls; body and DB actor authority |

The file correction covers the five native standalone protected-file routes.
It does not synthesize multipart body metadata or per-operation batch contexts.
The special-use avatar-IP policy remains an open compatibility/security review
candidate, separate from the corrected redirect cap.

All executions used Go 1.26.11, `GOMAXPROCS=2`; integrations used a fresh,
TCP-only PostgreSQL 17.9 cluster on port 55438, removed after testing.

| Command/scope | Result |
| --- | --- |
| `go test -race ./internal/userauth ./internal/api ./internal/accessrule ./internal/filestore -count=1 -json` | **344 top-level tests / 833 passing events; zero failures/skips** |
| `go test -race -tags=integration ./internal/userauth ./internal/filestore ./pgbase -run '^(TestAuthRecordHTTPRulesUseActualRequestMetadata\|TestAuthSessionHTTPRulesUseActualRequestMetadata\|TestAuthRuleTransportContextDoesNotSupplyActorOrBody\|TestAuthRulesUseTheProductionSQLBinding\|TestProtectedFileHTTPRetainsRequestRuleContext\|TestStorageControllerRangeRetainsLeaseUntilBodyClose\|TestUploadFailureCleanupBoundsSlowDeletionAndRetainsDurableIntent\|TestUploadFailureCleanupBoundsExhaustedPool\|TestVariantCleanupBoundsPublisherWaitAndPreservesLaterCommit)$' -count=1 -json` | **9 top-level tests / 72 passing events; zero failures/skips** |

A second test-fixture mistake used an undeclared body field and failed schema
resolution. It was changed to the built-in email field; the failed fixture log
and the complete successful rerun are both retained. No failed test was skipped
or acceptance criterion weakened. The tests ran on the final relevant runtime
and test blobs before their commit, without subsequent changes to those blobs.

Backend agent `/root/backend` independently reviewed immutable `86dbfa82`,
tracing wrapper placement, auth route families, recursive operand detection,
DB actor reload and body ownership. It reported no blocker. This was source
review, not an independent execution. Security independently source-reviewed
Backend `34be2bbe` (JSON encoding before extension transaction commit), including
owned/savepoint rollback and finite-value controls, and found no blocker.
The agents suspended compilation/testing for the Performance timing window.

## Latest hosted security evidence

Run [43804114855](https://github.com/yakisoba0728/PgBase/actions/runs/43804114855)
tested PR merge source `f763e5016bcdecf939cb307bd4c1b292a0306529` associated with
head `d77fd45d`. The new runtime above is **not** qualified by that old run.

The five source/image/SBOM/runtime-asset ZIPs were fetched afresh and every ZIP
SHA-256 matched the GitHub artifact digest. Source summaries identify the actual
merge SHA and report no diff-range restriction, with 483 Go and 330 JS/TS files
successfully extracted. The 24 Go and one JS finding identities match the
previously reviewed set by rule, message and partial fingerprint. All files
named in the newly retrieved reported paths have unchanged Git blobs relative
to the previously reviewed `9934a687` runtime. Exact JS result records are
unchanged; Go SARIF indexing and flow records differ, so the report does **not**
claim byte-for-byte identical Go dataflows or a new exhaustive path review.

All 32 all-severity image records and their rule descriptions match the prior
scan. Existing native-helper/dependency limitations in `final-security.md`
remain; no finding was suppressed. The actual copied trust bundle contains
148 unique certificate fingerprints matching its manifest and unchanged
bundle bytes. The copied timezone source is unchanged at `2026f`. The new image
ID is `sha256:e2e04b418a751718c6f2fe669669b134118a3098770e30e53fec122f9309e8b5`.
Dependency/source job logs and raw ZIPs are retained with the reconciliation.

## Evidence and remaining gates

[`evidence/session-security/manifest.json`](evidence/session-security/manifest.json)
records source/test identities and outcomes. The evidence directory contains
before/after and fixture-failure logs, full unit logs, original hosted artifacts,
job logs, artifact hashes, and the source/image/runtime reconciliation JSON.

Fresh integrated Go/race/PG16/17/18, browser/SDK/security and sustained-load gates
remain the Coordinator's responsibility. Independent execution of the new auth
regression, OAuth/MFA end-to-end request contexts, arbitrary expression policies,
file metadata outside standalone routes, avatar special-address policy and
native dependency exploitability remain explicit limits. Overall status stays
**Not Ready** while required integrated/performance gates are unresolved.
