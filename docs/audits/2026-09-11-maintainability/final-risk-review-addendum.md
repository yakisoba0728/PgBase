# Final targeted risk review addendum

Base: `5cb721ff5590bf89b45ea9087d2ace0b74fe7127`  
Reviewer: `sub-sol-final-risk-review`

This addendum resolves 16 disputed causes in the draft register. It preserves the earlier 19-entry `final-risk-review.json`, does not claim another full audit, and changes no product or test source. Existing green package tests are not treated as reproduction; every `reproduction` field remains false because no precise failing assertion was added and executed here.

| Finding | Disposition | Priority | Classification | Phase | Decisive result |
|---|---|---:|---|---:|---|
| ARL-P1-001 / RATELIMIT-001 | Accept | P2 | Static defect | 1 | `ExcludedIPs` remains caller-aliased across atomic publication, allowing silent bypass-policy mutation and a race. |
| ARL-P1-002 / RATELIMIT-002 | Accept | P3 | Static defect | 2 | The fallback branch returns the earlier error instead of the actual retry-query error; enforcement still fails closed. |
| UI2-001 | Accept | P2 | Static defect | 3 | Setup polling fulfillment lacks any lifetime/generation guard and can clear a newer restore job. |
| UI2-002 | Accept | P2 | Static defect | 3 | Backup poll callbacks mutate after abort; passing a signal to a transport is not a callback ownership guard. |
| UI2-003 | Accept | P2 | Static defect | 3 | Public file fetches convert non-2xx responses to blobs without checking `Response.ok`. |
| UI2-004 | Accept | P3 | Static defect | 2 | The final preview URL is never revoked on unmount, unlike the adjacent atomic-file preview implementation. |
| SDK-01-001 | Accept | P1 | Static defect | 3 | Dart shared realtime has neither an HTTP status gate nor a readiness-error path, so terminal handshakes leave `subscribe` pending forever. |
| SDK-01-003 | Accept | P2 | Static defect | 3 | TypeScript promises required `oauth2` while its own supported response fixture omits it and runtime performs no normalization. |
| EX1-P1-001 | Accept | P3 | Static defect | 3 | The old SSE loop reads the new loop's global controller in `catch`, producing stale failure UI. |
| EX1-P1-002 | Accept | P3 | Static defect | 3 | Rejected logout is an unhandled promise and leaves the example controls unexplained. |
| API-RT-001 | Accept | P2 | Static defect | 2 | Synchronous cleanup deliberately detaches from the request but supplies no replacement deadline, so storage can hold the handler indefinitely. |
| P1/P2-CONFIG-RETENTION-RANGE | Accept | P3 | Static defect | 1 | Environment validation admits a value the persisted storage contract deterministically rejects later. |
| PASS1/P2-USERMIGRATE-01 | Accept | P2 | Static defect | 1 | `written` rows bypass all artifact verification even though exact repair bytes are durable in the database. |
| PGB02-003 | Accept | P3 | Static defect | 3 | `gzip;q=0.00` is valid and explicitly refuses gzip, but the parser accepts it. |
| ROOTCI-P1-001 / PASS2-ROOTCI-001 | Accept, narrow | P3 | Static defect | 1 | Invalid DNS labels pass local config validation; this is operator-input validation, with no demonstrated security bypass. |
| PGB02-001 | Narrow | P3 | Maintenance | 4 | `io.ReadAll` defeats streaming, but object-size enforcement bounds allocation; range compatibility needs characterization before redesign. |

The prior `No independent adjudication evidence` decisions for the two rate-limit findings are overruled. Both effects follow directly from the assigned source: one publishes mutable caller storage to concurrent readers, and the other returns the wrong error variable on a reachable two-error branch.

P1 remains only for the Dart public SDK hang. The UI polling findings are concrete defects but affect stale administrative presentation, and both example findings are confined to sample behavior. The hostname finding is validation hardening for operator-controlled input, not evidence of attacker-directed certificate issuance.
