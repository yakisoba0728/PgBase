# Windows self-update acceptance

Date: 2026-09-22

The Windows updater was exercised on native Windows with task-owned fixture
installations only. No installed PgBase binary, service, unrelated process, or
Docker workload was changed.

## Accepted lifecycle

- A locally generated Ed25519 key signed an ephemeral release manifest served
  by a loopback TLS test server.
- An old Windows PE fixture downloaded and verified the new fixture, returned
  the durable `replacement_started` handoff state, and launched a separate
  helper process.
- The helper replaced the old binary, probed the canonical path, and committed
  the journal. Explicit rollback restored and probed the verified old backup.
- A candidate that succeeded under its staging name but failed its canonical
  health probe caused the helper to restore the old binary and persist
  `rolled_back`.
- A serve lifetime lock caused concurrent apply to return `replace_busy`.
- Recovery tests cover rollback publication before copy, after copy, and after
  rename. First-time and existing-target Windows publication are both tested.

The `replacement_started` result is the supported Windows restart-needed
case: the invoking process has staged and journalled the update, and the
post-exit helper owns completion. `update status --json` exposes the eventual
`committed` or `rolled_back` state.

## Defects corrected

Native execution found that Windows' synthetic Unix permission bits rejected
ordinary files, byte-zero locking interfered with executable loading, and an
open executable lock prevented replacement. It also found that `ReplaceFileW`
cannot create the first journal, candidate and backup paths without `.exe`
cannot be probed, and syncing a read-only Windows handle fails. Windows now
validates the actual owner and DACL, rejecting write-capable grants outside the
current user, SYSTEM, Administrators, and TrustedInstaller. A migration-safe adjacent lock file
holds serve/update exclusion across executable rename: pre-lock-file servers
remain fenced by the old executable lock while the updater creates and locks
the stable file, and later servers use that file directly. Unix keeps its
read-only executable-inode lock. Publication falls back to write-through
`MoveFileEx`, staged executables retain `.exe`, and syncing uses a writable
handle.

## Verification

```text
go test ./internal/selfupdate ./internal/cli
ok github.com/yakisoba0728/PgBase/internal/selfupdate
ok github.com/yakisoba0728/PgBase/internal/cli
```

The native subprocess acceptance is
`TestWindowsNativeUpdateLifecycle`; focused lock and publication regressions
are `TestExecutableLockAllowsProbe` and
`TestWindowsReplaceFilePublishesNewAndExistingTargets`.
