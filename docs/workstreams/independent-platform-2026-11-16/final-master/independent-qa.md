# Final master independent QA

## Decision and independence

**SCOPED_INDEPENDENT_QA_PASS** for the four bounded correction commits below. One separate reviewer (`/root/independent_qa`) inspected the changed source and independently executed original-source RED and corrected-source GREEN checks in separate worktrees. This reviewer authored none of the production changes, did not modify implementation files, and did not rely solely on the authors' logs.

This is approval for integration of the reviewed corrections, **not final release qualification**. The frozen combined candidate's full CI, PostgreSQL16.13/17.9/18.3 matrix, browser/MinIO/recovery/native-platform/security gates and200 RPS×600-second qualification remain separately owned by the Coordinator. Their exact final source and checkout identities must be reconciled in [final qualification](final-qualification.md). No broad security-zero or newly measured runtime-speed claim is made here.

## Exact independently reviewed sources

| Area | Local implementation commit | Exact checkout tree |
| --- | --- | --- |
| Original runtime for all RED checks | `242f80aea730ee483cc22798fb6450bdb3064081` | `b758d273142d75377c18011d240cd64929cb4c97` |
| Offline HTTP evidence validation | `6add53b53b57e6fc3826820ac7b6580b493629cb` | `a1abddc0078df9a34427225963bf0b6631930780` |
| TypeScript/Dart queued refresh | `8ccf8ca7b5a7a973dc209563e2b3256966da89be` | `519e2191f1c24eb27750d5970222d74e4e7354b8` |
| Storage-controller lease ownership | `e59fc07668e0a282555f2f52dbddf890162254e0` | `63b3b6eb3379e32967b16d5a637e9aa1d1052f95` |
| Administrator Jobs/Storage migration | `b56b74022eced2c767429b435a2abc4031ec37ee` | `2c6942b22a709d019b2cb18726ac8dc0b2d97887` |

These are the actual local implementation checkouts; they are not represented as published GitHub revisions. The Coordinator records their mapping to published commits. RED runs overlay the new regression fixtures onto unchanged original runtime files; those fixtures were not present in the original Git tree. Their hashes and actual runtime/source blobs are recorded in [source identities](../evidence/final-master/independent/reviewed-source-identities.json) and the execution JSON files. GREEN runs use the committed implementations.

## Independent execution results

| Check | Original source with new regression fixtures | Corrected source |
| --- | --- | --- |
| Performance evidence reader | Four invalid-evidence failures; one valid control passes |17/17 Python tests pass, including12 existing CodeQL parser tests |
| TypeScript session lifecycle | Two queued-replacement regressions fail |23/23 lifecycle tests pass; zero skips |
| Dart session lifecycle | Two queued-replacement regressions fail |26/26 lifecycle tests pass |
| Storage cancellation/foreign lease | Put/Open/Delete all fail target admission/object-preservation assertions |38 top-level filestore unit tests pass under race; zero skips |
| Physical two-controller storage | Foreign publication accepted; B reads/writes/deletes A's namespace |Correct namespace contents, deletion, publication owner and lease cleanup pass under race |
| Administrator Jobs/Migration |Seven failures; eight preservation controls pass |15/15 focused tests pass with unchanged assertions/timeouts |
| Existing adjacent storage/migration contracts |Existing regressions run on corrected source |Eight top-level tests + three subtests pass under race; zero skips |
| TypeScript generated distribution/public types |Not a RED check |Build + public type test + tracked/untracked distribution parity pass |
| Administrator generated distribution |Not a RED check |`npm run verify:dist` passes typecheck/build/clean-dist checks |

The reviewer also replayed three preserved real HTTP logs through original and corrected analysis modules. Every parsed metric remains exactly equal, with raw-file hashes recorded. This is offline replay, not a new HTTP workload or benchmark.

## Source-level findings and preserved behavior

### STORAGE-LEASE-01

The four added `lease.controller == c` conditions make Put/Open/Delete reuse consistent with BeginStorageOperation/OpenRange; GuardStoragePublication rejects a foreign controller before querying publication locks. The original source's failure was reproduced both with a canceled context and with two real local object namespaces backed by disposable PostgreSQL17.9. Corrected same-owner publication remains accepted, B's operations alter only B's objects, and both controllers' operation counts return to their expected values.

This is a trusted service-composition correctness defect. These checks do not demonstrate a public HTTP tenant-isolation exploit, and the priority is retained as P2. The fix does not alter database schema, object durability or tombstone retention.

Adjacent independent race checks cover staging admission release, lost publication ownership, retained range-reader leases, the four-slot request pool, late Put after lost sessions, legacy-worker/migration-source protection and schema68→69 queue preservation/reapplication. These tests also exercise fresh migration through69. Broader backup/restore, MinIO and PostgreSQL16/18 claims require final CI.

### SDK-REFRESH-01

TS and Dart now capture the queueing generation plus refresh lineage before waiting. An old refresh cannot borrow the replacement user's bearer. The reviewer checked the two-language conditions and corresponding auth-generation mutation paths. Preservation cases confirm three same-session rotations remain serialized, logout revokes the final rotated token, and a refresh explicitly issued by the replacement session can wait behind obsolete work and still succeed. Source and committed TypeScript distribution regenerate without drift.

### ADMIN-SESSION-01

Jobs and migration mutations capture the existing session scope at click/submit before the mutation queue runs. Success, error and settled callbacks check ownership before toast/cache/operation-state changes; migration previews from obsolete sessions cannot enable the start action. Same-session errors and actions remain covered. Source review found no layout, component hierarchy, styling or dependency changes, and a clean independent build reproduces committed distribution output. These guards cannot undo a server action already legitimately issued before a session change.

### PERF-EVIDENCE-01

The original experiment already registered c1→c8→c32→c4 recovery, complete request populations and request pool4. The new checks enforce those conditions; no performance threshold or error assertion is relaxed. All existing comparisons and classification thresholds remain intact. Three historical real logs retain identical parsed output. No new runtime optimization is credited to this offline validation correction.

## Environment and unsuccessful setup preserved

Independent commands used Go1.26.11, Node24.22.1, Dart3.13.3 and PostgreSQL17.9. Unit/race checks used GOMAXPROCS4; real PostgreSQL checks used GOMAXPROCS2 and Go `-p=1`. UI focused tests used `--maxWorkers=1` to bound shared-host contention, with original test assertions and timeouts unchanged. Installed project dependencies were copied into reviewer-owned directories; implementation worktrees were not modified.

The first real PostgreSQL attempt failed before behavior because the locally compiled fixture lacked `pg_trgm`; both old and new sources stopped at migration66. That is **BLOCKED_BEFORE_BEHAVIOR**, not valid runtime RED. The failed logs and environment remain preserved. After the Coordinator installed the source-matched extension, the reviewer ran one corrected-fixture old/new comparison and the adjacent suite. PostgreSQL reports17.9, fsync/on, full_page_writes/on and synchronous_commit/on. Each cluster starts and stops within one process lifetime and is removed afterward. No operational database or credentials were used.

The author's unrelated full-UI ApiView timeout remains separate historical failure evidence. This reviewer did not silently rerun the whole UI suite or declare that root cause proven. The frozen final complete UI CI must pass before the Coordinator can recommend manual merge.

## Reproduction and evidence

All raw logs, commands, exit codes, source identities, hashes and classifications are in [the independent evidence manifest](../evidence/final-master/independent/manifest.json). Large new logs are losslessly gzip-compressed with separate original-byte and compressed-byte hashes; existing historical repository evidence was untouched.

Key command families:

```sh
python3 -m unittest discover -s scripts/tests -v
node --test test/user-lifecycle.test.mjs
dart test test/user_lifecycle_test.dart --reporter expanded
go test -race ./internal/filestore -count=1 -v
go test -race -p=1 -tags=integration ./internal/filestore -run '^TestStorageControllerForeignLeaseKeepsObjectNamespaceAndPublicationOwner$' -count=1 -v -timeout=3m
node_modules/.bin/vitest run src/ops/JobsView.test.tsx src/storage/MigrationView.test.tsx --maxWorkers=1
npm run verify:dist
```

The exact adjacent test expression and fixture start/test/stop commands are recorded in JSON and preserved executable-source copies. Tests must use disposable databases with pg_trgm available.

## Integration conditions

The reviewed corrections are acceptable for integration without further speculative refactoring. Before a final READY decision, the Coordinator must verify that published candidate source blobs match these reviewed ones, all required fresh gates finish successfully on the actual candidate/CI checkout tree, the retained security/operational dispositions remain applicable, and main/PR state remains within the authorized policy. This reviewer started no workflow, sustained load gate, remote write, merge, release or deployment.
