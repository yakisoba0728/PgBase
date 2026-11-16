# Agent B — bounded Jobs correctness result

**One reproduced root cause fixed.** Author-tested source:
`47375c87669ea208813a1d4b1af71ef710db1d3b` (local worktree commit), based on
`55f539895f85ca3b7d8731ab8f4c3b8999ab92bb`.

## Boundary and defect

Initial bounded reads covered transaction/savepoint outcomes, Storage cleanup
ownership, and Jobs management fencing. Existing Jobs tests protected accepted
operator edits from stale completions but omitted the next occurrence identity.

After failure or lease expiry, changing the schedule/timezone changed
`next_run_at` while retaining positive `attempts` and `last_scheduled_at`.
The next claim reused the previous occurrence key, so a handler deduplicating
successful effects by that key could discard the new scheduled occurrence.

Schedule and Configure now atomically clear retry state only when the schedule
or timezone changes. Unchanged saves and requested retries preserve identity.
Existing token/generation fencing and schema 69 remain intact.

## Evidence

- `red.log`: unchanged base production code plus the new regression; all eight
  reschedule cases failed. Unchanged retry control passed.
- `green-integration.log`: PostgreSQL 17.9, all 18 Jobs top-level tests and
  20 subtests passed, including lease/admin fencing and the new boundary.
- `green-race.log`: same 18 tests/20 subtests passed under `-race`.
- `vet.log`: focused integration-tagged `go vet`, exit 0.
- `source-manifest.json`: exact author source hashes.
- `environment-failure.log`: preserved initial cross-exec fixture failure;
  explicitly excluded from defect evidence. `run-postgres.sh` corrected fixture
  lifetime by running the isolated server and test command in one invocation.

Independent QA and PostgreSQL 16/18 validation belong to the coordinator's
integrated-candidate phase. No other functional defect is asserted. No Storage,
transaction, or Realtime coverage beyond the stated bounded reads is claimed.
Firefox, performance, production access, remote publication, and UI changes
were not performed by this agent. Overall product remains **Not Ready**.
