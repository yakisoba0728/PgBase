# Independent PostgreSQL platform workstream

Draft PR [#148](https://github.com/yakisoba0728/PgBase/pull/148) on `refactor/independent-platform-20261116`, based on main `8f551de4`. It adds system migrations `000068` (realtime pending queue) and `000069` (durable variant cleanup).

## Status: blocked, not ready to merge

The last full qualification of candidate `89e883fc` ([run 43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611)) passed 14 of 15 jobs. The `mixed-http` sustained-load gate failed:

| Metric | Result | Gate |
| --- | --- | --- |
| Offered load | 200 RPS × 600 s | unchanged |
| Completed / dropped / errors | 120,000 / 0 / 0 | 120,000 / 0 / 0 |
| End-to-end p99 | **588 ms** | ≤ 250 ms |

Sampled evidence places the waiting in durable PostgreSQL `COMMIT` (WAL) and the request pool queue behind it. No correctable software defect was reproduced and no runtime patch was adopted.

Next step: capture a short trace with per-transaction acquire, `COMMIT` and release times aligned to PostgreSQL and host observations, establish an avoidable software cause, then make a minimal fix and run one full qualification on the new source. Do not rerun the unchanged candidate to look for a faster host.

## Documents

| Document | Purpose |
| --- | --- |
| [Architecture and migration](architecture-and-migration.md) | Authentication generations, query ownership and byte accounting, mail rendering, compatibility guidance |
| [Migration and rollback](final-master/migration-and-rollback.md) | Schema 68/69 upgrade order, rollback limits, connection budget |
| [Risk register](final-master/risk-register.md) | Open security findings and operational limits |
| [SDK cookie contract](../../sdk-auth-cookies.md) | `pgbase_auth` defaults and TypeScript/Dart cookie semantics |

The phase reports, qualification records and raw evidence (about 29 MB) were removed from the branch tip. They remain readable at commit [`3c163037`](https://github.com/yakisoba0728/PgBase/tree/3c163037e77c541eb94f46d137a479c45bc7338d/docs/workstreams/independent-platform-2026-11-16).
