# Independent PostgreSQL platform workstream

Merged to main as `d64c729f` through [PR #148](https://github.com/yakisoba0728/PgBase/pull/148) on 2026-11-17. It adds system migrations `000068` (realtime pending queue) and `000069` (durable variant cleanup). Production still runs schema 67; deployment is a separate step.

## Status: merged

The earlier candidate `89e883fc` failed the `mixed-http` sustained-load gate ([run 43982303611](https://github.com/yakisoba0728/PgBase/actions/runs/43982303611)). The merged source `a3ac4b5d` passed all 15 jobs in [run 44053449983](https://github.com/yakisoba0728/PgBase/actions/runs/44053449983):

| Metric | `89e883fc` | `a3ac4b5d` | Gate |
| --- | --- | --- | --- |
| Offered load | 200 RPS × 600 s | 200 RPS × 600 s | unchanged |
| Completed / dropped / errors | 120,000 / 0 / 0 | 120,000 / 0 / 0 | 120,000 / 0 / 0 |
| End-to-end p99 | 588 ms | **13.2 ms** | ≤ 250 ms |

The passing result is one run and the two hosts' fsync speed was not compared, so it mixes the fix below with host variance.

Sampled evidence places the waiting in durable PostgreSQL `COMMIT` (WAL) and the request pool queue behind it.

### Read commits no longer wait for a WAL flush

Statement statistics showed that read requests write WAL: the `FOR SHARE` pins on `schema_state`, `collection_access`, `users` and `collection_rules` assign an XID, so every read `COMMIT` waited for an fsync while holding one of four pooled connections. Record reads (`List`, `ListPocketBase`, `Get`) that own their transaction now commit with `synchronous_commit = off`. They write no rows, so nothing durable is at stake; the locks and their exclusion guarantees are unchanged. Mutations and reads inside a caller's transaction stay synchronous.

Local measurement (PostgreSQL 17.9 in Docker, 200 RPS × 30 s, pool 4, base and fix interleaved):

| | WAL syncs per run | p99 over 10 pairs |
| --- | --- | --- |
| Before | about 12,300 | 14–360 ms, median 70 ms |
| After | about 8,100 | 9–68 ms, median 58 ms |

The fix had the lower p99 in 9 of 10 pairs. Run-to-run variance on this host is large and its fsync (0.2–0.4 ms mean) is faster than the failed CI host (0.80 ms), so these local runs alone do not show that the 600 s gate passes; the CI run above does, once.

Still open: each mutation causes about 2.5 WAL flushes (the record transaction plus outbox delivery). Relaxing any of those changes delivery or durability semantics and is an owner decision.

## Documents

| Document | Purpose |
| --- | --- |
| [Architecture and migration](architecture-and-migration.md) | Authentication generations, query ownership and byte accounting, mail rendering, compatibility guidance |
| [Migration and rollback](final-master/migration-and-rollback.md) | Schema 68/69 upgrade order, rollback limits, connection budget |
| [Risk register](final-master/risk-register.md) | Open security findings and operational limits |
| [SDK cookie contract](../../sdk-auth-cookies.md) | `pgbase_auth` defaults and TypeScript/Dart cookie semantics |

The phase reports, qualification records and raw evidence (about 29 MB) were removed from the branch tip. They remain readable at commit [`3c163037`](https://github.com/yakisoba0728/PgBase/tree/3c163037e77c541eb94f46d137a479c45bc7338d/docs/workstreams/independent-platform-2026-11-16).
