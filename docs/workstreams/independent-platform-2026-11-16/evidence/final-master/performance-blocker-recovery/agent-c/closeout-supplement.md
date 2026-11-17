# Independent blocked-closeout supplement

**APPROVED FOR DOCUMENTED BLOCKED CLOSEOUT. No runtime correction or new final performance run is approved.**

The reviewer independently inspected Agent B's raw Go JSON events, exact runner, recorded source/tree, PostgreSQL settings before/after, cleanup output and shutdown logs in the separate QA worktree. The original test definitions were read at the recorded starting source `77bc070047c5a19d68439b75cb7b65bcde5d1c9a`; their blob identities and raw evidence SHA-256 hashes are in [focused-controls-independent-verification.json](focused-controls-independent-verification.json).

[verify_focused_controls.py](verify_focused_controls.py) recalculates the counts directly from JSON events rather than trusting Agent B's summary:

- Exactly six expected parent tests and three read-query subtests pass across `internal/database`, `internal/schema` and `internal/query`.
- No failure or skip events exist in the successful control run; exit status is zero and stderr is empty.
- The retained command selects integration tests once with the race detector, `-p 1`, three-minute timeout and `GOMAXPROCS=4`. No load benchmark is selected.
- Two listener output lines confirm four simultaneously held request connections, five total fixture clients including its separate listener, and all three durability settings ON.
- PostgreSQL 17.9 settings before/after retain `fsync`, `synchronous_commit` and `full_page_writes` ON. Cleanup returns no remaining test-database rows, and both control output and server log confirm PostgreSQL shutdown. Removal of the temporary data directory is not claimed.
- All six parent failures in the retained initial attempt report connection refusal at test-database initialization. They are fixture failures and cannot serve as an application defect RED case.

Source inspection confirms what these controls assert: physical rollback on callback error/panic; connection release after deadline rollback; preserved pool capacity across listener reconnect/shutdown; DDL waiting for a record schema pin; and fixed query ceilings for registered scalar, multi-relation and nested-expansion cases. None measures 200 RPS sustained latency. Only the listener control explicitly forces a four-connection pool; other controls retain their existing fixture settings.

The root recovery report was read independently for factual, causal and source-binding consistency. Its BLOCKED decision, distinction between sampled COMMIT age and wait duration, 1,038 active-WAL/full-pool count, 117 COMMIT-blocker edges, source equivalence, preserved failures, narrow control scope and conditional next-step boundary are supported. No factual or causal correction is required for the reviewed report. Exact final publication SHA/tree must still be recorded after publication; no current focused control result is represented as a new whole-source CI pass.

This supplement approves truthful closure of the bounded investigation. It does not endorse merge readiness, a runtime patch, removal of schema/transaction locks, or another unchanged-source final gate. No additional database, race suite, benchmark or scanner was executed by the reviewer during this supplemental check.
