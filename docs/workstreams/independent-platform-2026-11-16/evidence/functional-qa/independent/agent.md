# Independent QA result

**ACCEPT_SCOPED_CHANGES; overall product Not Ready.** Reviewer
`/root/independent_qa` authored none of the runtime fixes and used a separate
worktree. Tested local SHA: `ecff2d1ff1ddca96211832908b1cfb6c40faf8e3`;
tree: `fd5f4c233f621e35badd9a5e401f5e090ddf310b`.

Reviewed the auth compiler's recursive missing-metadata guard, Jobs atomic
occurrence reset versus unchanged retry/fencing, and TS/Dart iterator generation
checks at buffered delivery and next-page requests. No blocking defect found.
No workflow, UI, schema or existing-test weakening changes were present.

Independently reversing only the auth compiler to base source reproduced six
missing-metadata subcase failures plus batch/replay failures. Nine real-context
and body-only controls passed. Fixed source was restored before GREEN runs.

Actual independent executions:

- PostgreSQL 16.13, 17.9, 18.3: each 169 top-level +129 subtest pass events.
- PostgreSQL 17.9 `-race`: same 169+129 passed within unchanged timeout.
- Selected real HTTP: five top-level +40 subtests, including both SDKs.
- TypeScript 119; Dart 138; zero failures/skips in accepted runs.
- Related `go vet` and `dart analyze`: successful.

Initial PG16/18 fixtures lacked `pg_trgm`; failure logs remain separate from
product results. Installing matching extensions fixed the fixtures; no product
change was used. Detailed review, commands, source hashes, outcomes and
losslessly compressed logs are retained in this directory.

The auth reproduction is an embedded/service/batch boundary, not proof of an
external HTTP exploit. External Jobs effects still need idempotency/fencing.
Firefox, performance and full CI were excluded. No runtime edits, additional
defect search or remote writes were performed by this reviewer.
