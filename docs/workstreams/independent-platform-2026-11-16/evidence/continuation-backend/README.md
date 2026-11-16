# Backend recovery evidence

The source and scope ledger is [continuation-backend.md](../../continuation-backend.md).
`manifest.json` records the final source commit/tree, exact command, test event
counts, environment and SHA-256 of every retained raw result.

| File | Meaning |
| --- | --- |
| `relation-filter-before.log` | Both transient current-record failures incorrectly advance the cursor on the published starting runtime plus the new test. |
| `relation-filter-after.log` | Focused relation-filter failures, replay recovery and genuine nonmatch pass race ×3. |
| `realtime-after-race.log` | Broader multiplex, slow-client, reauthentication and queued-message selection on `f6c56308`, race ×3. |
| `expansion-before.log` | Actual PostgreSQL policy-context discrepancy: direct expansion succeeds and SSE omits it. |
| `expansion-after.log` | PostgreSQL allowed/denied-context equivalence passes race ×3 after `59a854cc`. |
| `expansion-fault-before.log` | Expansion pool/connection errors incorrectly acknowledge a partial event; missing/denied-row controls pass. |
| `current-record-fault-after.log` | Final relation-filter and expansion fault/recovery behavior passes race ×3. |
| `final-focused.jsonl` | Raw Go JSON events from exact final runtime `7779f097`: 15 top-level /39 total pass events, no fail/skip, package 2.159 s. |

Before tests were intentionally added to the preceding runtime before its fix;
their trees were therefore dirty test-only reproductions, not newly published
revisions. The manifest states each source relationship instead of assigning
the eventual fixed commit to a failed-before run. Transient error codes in the
unit delivery tests are injected at the RecordService interface. The policy
context integration case uses real schema, rules, records and PostgreSQL 17.9.
Neither is reported as a production network or full-database restart test.
