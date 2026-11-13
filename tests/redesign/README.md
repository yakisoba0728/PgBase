# Historical schema 66 acceptance

`TestSchema66RedesignUpgrade` builds source commit
`763c214a754461fe244349979a55e649f875aacd` with `git archive`, then creates
and populates a disposable database using that binary. It does not generate the
historical fixture with the candidate under test.

```sh
GOTOOLCHAIN=go1.26.11 \
PGBASE_REDESIGN_ARTIFACTS=.local/redesign/schema66-run-1 \
go test -tags='restore upgrade' ./tests/restore \
  -run '^TestSchema66RedesignUpgrade$' -count=1 -v
```

The default PostgreSQL 16/17/18 matrix is the existing digest-pinned restore
matrix. `PGBASE_RESTORE_POSTGRES_IMAGES` may narrow a local diagnostic run; a
narrow run does not establish full matrix acceptance.

The source instance is stopped before the dump and file copy. The fixture
contains administrator/user sessions, a separate auth realm, UUID and exact
text IDs, single/multiple relations, revision/ETag, audit/outbox/idempotency,
local file bytes and metadata, original raw `pocketbase` rule source, and a
disabled durable scheduler row. The disabled row has no executable handler.

Before starting the new binary, the test retains the old dump and files with a
manifest containing the source commit/tree, binary SHA256, PostgreSQL image,
artifact SHA256 values, and semantic snapshots. An existing artifact directory
is never overwritten: use a fresh run directory. Relative artifact roots are
resolved against the repository root. Without the variable, artifacts live in
the test's temporary directory.

Generated manifests contain disposable fixture credentials needed to preserve
sessions. Keep these artifacts under ignored `.local/`; do not commit or upload
them as production data. The manifest is private to the local test run.

The new candidate applies forward migrations to the restored database, checks
preserved SQL state, and runs the existing doctor/file/session/relation/replay
restore acceptance. Additional HTTP checks cover external text identity and
its ETag, canonical expression source, and the separate realm's records. The
baseline must report schema 66; the candidate may keep schema 66 when no schema
change is necessary. This is an upgrade/restore acceptance test, not deployment
or a promise that an old binary can run on a newly migrated database.
