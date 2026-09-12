# N9 self-update independent review

검토 시각은 2026-09-10 06:18–06:30 KST다. 독립 검토 branch/worktree는 `codex/n9-review` / `review-n9`이며, owner branch의 dirty 파일을 섞지 않고 각 커밋을 detached 임시 worktree에서 검증했다.

| 항목 | 값 |
| --- | --- |
| fixed base | `91a0bdac9212cdf26207cdba619fc579b359b885` |
| 최초 stable core | `b0c03f4da4c3075a59684edd0eab4a730207133e` |
| 최종 N9-private 후보 | `9f81ecf59ad0c9dadf9d62f8bbe8ee51ce0d3db5` |
| owner | Sagan / Terra, `codex/n9-self-update`, `n9` |
| lead | Linnaeus, task `01a08772-f4a5-7de1-8369-0df983c24a59` |
| host | macOS 27.0, arm64, Go 1.26.8 |

## Verdict

**Standards: PASS, open findings 0.** `91a0bda..9f81ecf` is confined to `docs/workstreams/n6-n9/N9.md`, `internal/buildinfo`, N9-private CLI handlers, `internal/selfupdate`, platform files and focused tests. It does not change `go.mod`, `go.sum`, migrations, shared status/integration documents, installed files or shared command dispatch. The largest N9 production file is `internal/selfupdate/install.go` at 621 lines; `internal/cli/update.go` is 369 lines. `git diff --check`, focused vet and the pinned N9-core lint pass with zero findings.

**Spec: PASS for the N9-private owner scope, open findings 0.** Exact-byte Ed25519 verification, signed manifest validation and platform selection, explicit trust key handling, bounded local/HTTPS retrieval, redirect/origin/cancellation/deadline behavior, streaming size/SHA verification, installation ownership and symlink checks, locks, fsynced evidence/journal/backup, native replacement/probe/recovery/rollback, schema/error contracts and Windows helper compilation are implemented and exercised within the authorized local boundary.

The lead-owned shared composition is intentionally outside this verdict. The owner branch does not register `version`/`update` in the shared command table, hold `AcquireServeLock` across `runServe`, or supply final common release build wiring/help golden. A directly built owner-branch `pgbase version --json` therefore still reports an unknown command. The lead is integrating those seams separately; this is not an N9-private defect or blocker.

## Findings and exact re-review

The initial stable core compiled and its focused race tests passed, but review returned concrete conditions to the owner. Every condition below was reproduced in a temporary worktree and rerun after the named fix.

| Finding | Exact condition and source | Resolution and re-review |
| --- | --- | --- |
| Duplicate signed JSON keys and malformed artifact URL accepted | duplicate `schemaVersion` key, or a signed unselected artifact URL with a fragment, passed verification | `cb9584f`/`4ed78a3`; duplicate-key rejection is at `internal/selfupdate/manifest.go:35`, artifact URL validation at `:63` and `:127`; exact tests pass |
| `rollback_started` crash was not recoverable | before-copy, after-copy and after-rename states returned recovery failure | `4ed78a3`, `fe53db0`; `internal/selfupdate/install.go:333`; all three crash edges pass through `Recover` |
| Unsafe installation state accepted | symlinked lock and world-writable existing state could be used | `4ed78a3`, `46bb24d`, `4aeb22e`; lock validation at `internal/selfupdate/install.go:438-464`, Unix ownership/mode predicates in `internal/selfupdate/state_unix.go:10-27`; exact tests pass |
| Journal target was not bound | a journal created for `pgbase-a` could be applied to `pgbase-b` in the same directory | `2e48d07`; target checks at `internal/selfupdate/install.go:237`, `:265`, `:288`; foreign-target test passes |
| Installation target/directory mutation seam | group/other-writable target or parent and a changed target identity could reach publication | `46bb24d`, `4aeb22e`; ownership checks at `internal/selfupdate/install.go:486-491` and pre-publication identity check; both writable-path cases pass |
| Source build bypassed `minUpdaterVersion` | current `dev/unknown`, exact requested `v0.2.0`, signed minimum `v99.0.0` selected successfully | `bf36f06`; fail-closed selection at `internal/selfupdate/select.go:66`; exact repro passes |
| Large numeric prerelease IDs misordered | `...516` and `...517` overflowed `uint64` and the lower release was selected | `bf36f06`; arbitrary-length numeric comparison at `internal/selfupdate/semver.go:82-99`; exact repro passes |
| CLI apply blocked recovery | every unresolved journal returned `recovery_required` before the library recovery path | `0a5d9b5`, `e7dd638`; locked `Recover` at `internal/selfupdate/install.go:246-272`, recovered apply return at `internal/cli/update.go:143-148`; replacement-started old/new target repros pass |
| Rollback schema checked candidate against itself | committed Old schema 40/New schema 41 under running schema 41 allowed restoration of Old 40 | `0a5d9b5`, `e7dd638`; journaled `Previous` is checked at `internal/cli/update.go:207-219`; known mismatch is `migration_required`, unknown/no journal is `recovery_required` |
| Stable migration code absent | incompatible apply/rollback emitted `recovery_required` | `0a5d9b5`; `ErrMigrationRequired` and exact code are at `internal/selfupdate/types.go:23`, `:56`; permanent CLI test passes |
| Required evidence gaps | no explicit local artifact success, `BeforePhase` was unused, and HTTPS/body/hash/permission/ENOSPC fault evidence was incomplete | `589a612`, `d90fe94`, `d111ebd`, `72021e4`, `0e01915`, `262df62`, `0e26461`, `9f81ecf`; generated-key local apply/rollback, all durable phases, symlink artifact refusal, redirect/non-2xx/TLS/hash cleanup, ENOSPC propagation, short Content-Length, body deadline and permission preservation all pass under race |

Windows replacement uses `ReplaceFileW` at `internal/selfupdate/replace_windows.go:40-55`. The helper validates the parent wait, locks, target/candidate journal binding, digest/probe, replacement, rollback on post-replace failure and commit at `internal/selfupdate/windows_helper.go:16-82`.

## Verification evidence

All tests used `t.TempDir()` or `/tmp/pgbase-n9-review-final.aQfeiJ`, generated Ed25519 keys, directly built old/new fixture binaries and loopback TLS. The native fixture listened only on task-owned `127.0.0.1:58098`. No test used `8090`, `55440`, `59000`, the installed PgBase binary, UserData, Docker or another container.

Commands at final `9f81ecf`:

```text
GOTOOLCHAIN=go1.26.8 go test -race ./internal/selfupdate -count=1
GOTOOLCHAIN=go1.26.8 go test -race ./internal/cli -run 'TestRunVersionEmitsStableNetworkFreeJSON|TestCandidateCompatibilityUsesOnlyExplicitBuildSchema|TestRollbackSchemaUsesPreviousBuildAndStableMigrationCode|TestUpdateInputRequiresExplicitTrustAndSourceMode|TestLoadTrustKeyRejectsSymlinkAndAcceptsExactBase64' -count=1
GOTOOLCHAIN=go1.26.8 go vet ./internal/buildinfo ./internal/selfupdate ./internal/cli
GOTOOLCHAIN=go1.26.8 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./internal/buildinfo/... ./internal/selfupdate/...
```

Results: selfupdate race `ok` in 10.450s, private CLI race `ok` in 1.568s, vet exit 0, lint `0 issues`. The native tests execute the HTTPS and local exact-byte signed old→new replacement, mode/version/commit probe, failure rollback, explicit rollback, crash recovery, backup tamper, lock and path cases. The later evidence tests also execute local symlink rejection, all five `BeforePhase` phases, response body deadline, short declared Content-Length cleanup, non-2xx, redirect cap, untrusted TLS, hash mismatch cleanup, permission preservation and deterministic `ENOSPC` propagation.

Fresh CGO-free builds at final source used injected `v0.2.0`, commit `2222222222222222222222222222222222222222`, built-at `2026-09-10T00:00:00Z`, schema `40..40`:

| target | SHA-256 |
| --- | --- |
| darwin/amd64 | `94784723c6fe47989cd4b8374687287490ae75078aaefae84787eaa141b299f7` |
| darwin/arm64 | `48aa6300bae85afe0dab6f8011299dc42e75bb063485f14c514718ef37271425` |
| linux/amd64 | `1fb2bf711bdf0c7806a0c9400dd8ce338ccd409bc8044303d9317f4a3c400421` |
| linux/arm64 | `2323e1310751b65553248179e2e69953a0837c2ab27f857b696ebc1a84614c46` |
| windows/amd64 | `80d0a9866861329bba531f59d3c7352b9e784a34cb02ffb0633af8214738ba41` |
| windows/arm64 | `d7c4b3bcb0dd6ba4bd58cffde65fe7696c49f3edbfe89a89c370261c4af28878` |

Final Windows test-package compilation also passed for both `internal/selfupdate` and `internal/cli`:

| package target | SHA-256 |
| --- | --- |
| selfupdate windows/amd64 | `472fb5e2063a067281f253ac45b2b35130e34dc5c617d97f132f3d2ce276bfdd` |
| selfupdate windows/arm64 | `fcce6bffbf9cf9731896e47d4877381f0924bcc3d4a328decccc9c782a5500aa` |
| cli windows/amd64 | `df9d1b0133f87605da896d39090dac4635f6117c7cc848d84fde4afaa920a552` |
| cli windows/arm64 | `9e541737789d4f617706b940334f8b6396beadc564fdae9357e55db95af38c88` |

No Windows host was available. Parent-process wait, native sharing violation behavior, `ReplaceFileW`, preserved backup, helper crash/journal recovery and Windows old/new probes were compiled but not executed. This is the explicitly accepted host limitation and is not a local N9 blocker.
