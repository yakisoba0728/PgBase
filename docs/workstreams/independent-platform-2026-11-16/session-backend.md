# Subagent-first backend recovery — 2026-11-16

## Recovered runtime changes

Started from published `d77fd45de6975f55b047a3de9c4099e1805162ce`. The previous backend changes existed locally but were absent from that published source. No previous worktree was changed.

| Local recovered commit | Prior coordinator commit | Correction |
| --- | --- | --- |
| `f59d34c3` | `564d1919` | A transient current-record relation lookup failure now emits a sanitized stream error and retains the preceding replay cursor. A successful nonmatch still advances. |
| `9cf59607` | `1e005485` | Realtime expansions receive the current request policy context, matching ordinary reads. |
| `daf0649c` | `b17f83e2` | Transient expansion lookup failures remain retryable; missing or denied live rows preserve the already-authorized snapshot behavior. |
| `a3f6e8a8` | `ddc8c616` | Formats the policy-context integration test. |
| `787f9b02` | `eb139114` | Preserves the original failing/passing evidence and bounded coverage ledger. |

## Fresh verification

The [recovery manifest](evidence/session-backend/recovery-manifest.json) binds the exact tested source tree, test command, versions, counts, elapsed time, and raw-log digest. Focused PostgreSQL17.9/race tests passed three repetitions with 15 top-level executions, 39 total pass events, no failures and no skips. The package ran in 5.144 seconds. The database was newly initialized, loopback-only, and disposable. No production access occurred.

Read the [earlier scoped backend report](continuation-backend.md) for reproduced pre-fix failures. Those historical records are retained with their original source identities; they are not relabeled as executions of this recovery's commits.

## Remaining work

Independent cross-review and final integrated CI remain required. This work does not resolve the fixed-arrival HTTP failure. The extension transaction/cleanup audit is continuing separately. No full architecture certification, release readiness, or PostgreSQL16/18 qualification is claimed here.
