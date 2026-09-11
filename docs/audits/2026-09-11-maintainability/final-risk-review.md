# Final targeted risk review

Base: `5cb721ff5590bf89b45ea9087d2ace0b74fe7127`  
Reviewer: `sub-sol-final-risk-review`

This is a third, narrow adjudication of the assigned high-risk families. It is not a new full-codebase coverage claim. No product or test files were changed, and no finding is marked reproduced because this review did not add and execute a failing assertion for its precise trigger.

## Authoritative outcomes

| Finding | Final disposition | Priority | Classification | Decisive reason |
|---|---|---:|---|---|
| AR1-P1-001 / ACCESSRULE-001 | Narrow | P3 | Contract risk | The helper fails open for malformed internal subjects, but production callers construct or validate principals; no external path to an invalid subject was found. P1 is overruled. |
| AR1-P1-002 | Defer | P3 | Maintenance | Duplication creates drift risk but no present wrong result. |
| IUA-01 | Narrow | P2 | Static defect | Unsafe return URIs survive only in the split return/provider-redirect flow and require startup allowlisting. |
| IUA-02 | Defer | P3 | Contract risk | Direct update is a trusted administration primitive; retaining verification needs an explicit compatibility decision. |
| IUA-03 | Accept | P3 | Static defect | Validator and evaluator disagree on `emailVisibility`. |
| IUA2-01 | Narrow | P2 | Contract risk | OIDC Core says clients **SHOULD** check `azp` for multiple audiences; calling missing `azp` a universal MUST violation would be inaccurate. Strict checking is sound hardening for this fallback verifier. |
| AA1-P1-001 / ADMINAUTH-001 | Accept | P2 | Static defect | Startup config accepts unsafe/non-web schemes and puts a live token in the resulting mail link; there is no remote URL control. |
| AA1-P1-002 | Defer | P3 | Maintenance | Repetition is real, but no divergent rate-limit behavior is shown. |
| P1-JOBS-LEASE-IGNORING-HANDLER | Reject as bug | P3 | Maintenance | At-least-once execution permits a context-ignoring handler to overlap after lease expiry; generation fencing protects scheduler metadata, not external side effects. P1 is overruled. |
| P1-FILESTORE-STAGE-READ-ORPHAN | Accept | P2 | Static defect | A non-EOF validation read error is the sole post-Put error path that does not enqueue the otherwise undiscoverable object. P1 is overruled because impact is one orphan per exceptional backend failure. |
| P1/P2-DATABASE-ROLLBACK-EXPIRED-DEADLINE | Reject leak claim | P3 | Maintenance | pgx marks the transaction closed, kills an undefined connection after rollback failure, and pgxpool releases it. The defect is avoidable connection churn, not a leak. |
| PASS1/P2-SELFUPDATE-01 | Narrow | P3 | Contract risk | Current production use is a same-stack CLI call from `Fetch` to `Apply`; mutation requires trusted in-process code. The signed-selection package contract should still be enforced at `Apply`. P2 remote-exploit framing is overruled. |
| P1-internal-schema-01-001 | Reject | P3 | Reject | Import is merge based; omitted optional token purposes intentionally survive. |
| P1-internal-schema-01-002 / IS1-P2-001 | Accept | P2 | Static defect | Preview/apply can persist regex and rule strings without validating their runtime executable contract. |
| P1-internal-schema-02-001 / IS2-P2-001 | Accept | P2 | Static defect | TTL rows are absent from the stale-preview checksum and can be overwritten after an intervening committed edit. |
| PGB01-001 | Accept | P2 | Static defect | The output cap is applied only after `findAll` has accumulated and marshaled all records. |
| PGB01-002 | Defer | P3 | Maintenance | Bounded deletion would introduce observable partial progress and needs a contract choice. |
| PGB01-003 | Narrow | P3 | Static defect | Host reflection is real, but autocert host policy blocks certificate issuance; no higher-impact chain was demonstrated. |
| PGB01-004 | Accept, narrow | P2 | Static defect | First-caller cancellation controls shared graceful shutdown and its final error, while background cancellation and asynchronous pool close still proceed. Claims of complete cleanup loss are too broad. |

## Contract details that control implementation

The jobs scheduler is at least once. A regression should demonstrate that a stale worker cannot update scheduler metadata after a new fence generation; it should not demand exactly-once external side effects from arbitrary handlers.

The database rollback path does not leak a pgxpool lease when its cleanup context is already expired. pgx's rollback marks the transaction closed and kills the connection on error, and pgxpool releases the wrapper. A useful test measures pool acquisition returning to baseline and whether a replacement connection is needed.

The file-stage object is durably orphaned on the narrow `ReadFull` error path because metadata publication has not begun and the deletion queue is not written. The acceptance test must inject a non-EOF read failure after a successful `Put`; ordinary existing package tests are not reproduction evidence.

The schema importer has merge semantics, so omitted TTL purposes remain. Separately, any supplied TTL participates in the approved preview and therefore must participate in stale detection. Runtime regex and rule validation also belongs before persistence, with shared cycle-free validators so the importer and runtime cannot drift.

The first `Close` context currently controls termination hooks and graceful component shutdown. A canceled context does not mean nothing happens: background cancellation is invoked and `Pool.CloseContext` starts an asynchronous pool close before returning cancellation. The regression must observe each cleanup component, not merely the first caller's error.

The OIDC conclusion follows the normative wording in [OpenID Connect Core 1.0, ID Token Validation](https://openid.net/specs/openid-connect-core-1_0-18.html#IDTokenValidation): the multiple-audience `azp` check is a SHOULD. The implementation can choose strict rejection, but the acceptance test and compatibility note must describe that policy accurately.

Full caller, defense, source-line, impact, and acceptance-test details are in `final-risk-review.json`.
