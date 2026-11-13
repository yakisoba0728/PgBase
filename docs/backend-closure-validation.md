# Backend closure validation — 2026-11-14 KST

This report supersedes the validation status and open gates in [the historical follow-up report](backend-follow-up-validation.md). Its original failures remain preserved there. This is a bounded closure and validation record, not a universal security assurance.

## Scope and results

The closure ledger contains **195 provenance records**, including aliases, observations, contract decisions and documented limits. It is not a count of 195 distinct bugs, vulnerabilities or fixes. Owners added focused regressions for reproduced defects and retained the distinction between source review, executed tests and unresolved causes.

Corrections cover authorization and credential invalidation, exact numeric transport, record identities and transactional artifacts, relation/query policy propagation, realtime replay and current authentication context, staged files and storage transitions, backup transport and recovery tooling, JavaScript runtime contracts, SDKs and administrator UI behavior. Migration **67** is required for the final source state; run the normal explicit migration procedure before serving this version.

## Validation snapshot

| Check | Recorded outcome |
| --- | --- |
| Final Go unit suite with race detector | PASS (`final-cascade-unit-race.log`). |
| Final blocking lint and vet | PASS; lint reports **0 issues** (`final-cascade-lint.log`, `final-cascade-vet.log`). |
| PostgreSQL 16 | Full integration run PASS. Latest S11 auth-cascade/userauth follow-up also PASS. |
| PostgreSQL 17 | Initial full race run had three fixture failures. Subsequent full application run PASS (**306.884s**), full administrator-authentication run PASS (about **134s**), and doctor/migration-67 checks PASS. Initial failures remain part of the evidence. |
| PostgreSQL 18 | Initial application run returned HTTP 503; its cause was not proved. A normal full application replay PASS (**195.149s**), and the other package runs PASS. The replay does not establish the cause of the earlier failure or claim a full application race pass. Latest S11 userauth follow-up PASS. |
| Administrator UI | **251 tests** PASS. |
| SDKs | TypeScript **62 tests** and Dart **79 tests** PASS, with their recorded type/build/analyzer checks. |
| Browser acceptance | **19 checks**, additional focused browser checks and CSP checks PASS. These cover the exercised flows, not every browser or deployment. |

The final-cascade checks include the late public auth-cascade correction. Integration results above are separate runs; they must not be presented as one uninterrupted, failure-free matrix execution. Local detailed logs and scratch fixtures remain excluded from Git.

## Contracts to retain

- **Public auth deletion:** authorize the root and every affected auth cascade-delete target under each target realm's current delete rule, in the deletion transaction. Denied or null rules roll back the entire operation. Administrator/trusted deletion retains configured cascade authority; configured set-null effects retain their existing behavior. See [public API contracts](public-api.md).
- **Current deletion policy:** a policy captured before waiting for a record lock cannot authorize deletion after a concurrent rule change. Checks use the current locked target and policy.
- **Views:** declarative views inherit their source collection's admission and action rules. Attempts to save an independent policy that reads would ignore are rejected with HTTP 403. SQL views retain their own collection policy.
- **Database TLS:** the backup gate validates the parsed transport against the caller's explicit insecure-development policy before connecting. Ambiguous repeated TLS URL parameters and the `ssl` alias are rejected; use one canonical `sslmode`. Loopback development fixtures remain supported.
- **SDK projection:** native collection event streams carry mutation metadata and do not support record-field projection. Projected record payloads use multiplex subscriptions. A nonempty native-event `fields` option is rejected locally before HTTP, with guidance to multiplex subscriptions. Dart refresh also rejects nonempty `fields` before a single-use token can rotate; empty or omitted fields retain the complete-session contract. See [realtime contracts](realtime.md).
- **Jobs and multipart files:** omitting a job timezone preserves the stored zone. Multipart staging, attachment commit and cleanup share one request storage operation; the regression covers 65 files and rollback after a forced failure at the 65th file insert.

## Candidate artifact

The verified candidate is **rc.4**, digest:

```text
c8d3f44cc3ad6f8d2bf77727d7f0b7b04e7f6fe6da2353c4a8d005525c4f6da3
```

Final artifact verification passed for this same digest:

- Nonroot ARM64 image, schema version **67**; runtime build inputs unchanged during verification.
- Candidate end-to-end checks run twice in shuffled order: PASS (**41.746s**).
- PostgreSQL **16/17/18** restore checks: PASS (**19.299s**).
- Trivy reports **0 HIGH/CRITICAL** findings; SPDX artifact generated.
- Embedded administrator UI matches the exact **144-file** build output.

These artifact checks supplement the source/package results above; they do not constitute deployment approval or expand the tested platform scope.

## Limits

- **O3 remains a documented product limit:** official distroless images contain no PostgreSQL client tools. Managed backup jobs and offline create/restore require an operator runtime with matching `pg_dump`/`pg_restore` and their shared libraries. Keep managed backups disabled in the stock image; see [backup runtime requirements](backup-s3.md).
- Restore uses the same PgBase release that created the archive, followed by the separate upgrade procedure. Arbitrary cross-version archive activation is not promised.
- Passing tests do not prove external OAuth-provider interoperability, crash/power-loss durability, Windows runtime behavior, or universal security. Those were not established by this closure.
- The initial PostgreSQL 18 HTTP 503 cause remains unproved. Preserve it alongside the successful replay rather than assigning a speculative explanation.
