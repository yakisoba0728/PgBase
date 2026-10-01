# Administrator credential concurrency proof

Date: 2026-09-22. Scope: existing password recovery and email OTP operations interacting with the new administrator account management service. This is a native correctness change, with no PocketBase compatibility requirement.

## Contract and implementation

Account management takes the administrator lifecycle advisory transaction lock, checks the active actor, locks the target account, and removes its sessions and pending email credentials. Recovery and OTP operations now enter the same lifecycle lock before accessing their account or credential rows. Password hashing and mail transport remain outside that transaction.

- Password reset confirmation and email OTP completion cannot lock a pending credential while account management holds the target account lock and waits to delete that credential.
- Reset/OTP issuance checks active state and persists the credential within the same lifecycle transaction. A call queued behind deactivation rereads the committed inactive state. Public password recovery still returns an enumeration-safe success; explicit CLI OTP issuance returns invalid credentials.
- CLI password reset participates in the same lifecycle ordering.
- Email OTP completion returns database failures without replacing them with a credentials error or an aborted-transaction commit error.

Production anchors:

- `internal/adminauth/password_recovery.go:19` — CLI reset.
- `internal/adminauth/password_recovery.go:100` — reset issuance and active lookup.
- `internal/adminauth/password_recovery.go:156` — reset redemption.
- `internal/adminauth/email_otp.go:43` — OTP issuance.
- `internal/adminauth/email_otp.go:90` — OTP redemption and error propagation.

## Deterministic PostgreSQL regressions

The tests invoke real public service methods against isolated PostgreSQL databases. A test-only trigger pauses management at session deletion, after its target account lock. `pg_stat_activity` and `pg_locks` confirm both competing operations reached actual database lock waits before the gate is released. The trigger is not part of production code.

| Regression | Actual RED before its fix | Actual GREEN after its fix |
| --- | --- | --- |
| Management password change versus pending password reset | Management waited at the gate; redemption waited on the account transaction. PostgreSQL reported deadlock `40P01`. | Redemption waits on the lifecycle advisory lock, then returns invalid credentials after cleanup. The managed password authenticates successfully. |
| Management password change versus pending email OTP | Same reversed credential/account ordering; service returned `commit unexpectedly resulted in rollback` after swallowing the deadlock cause. | Lifecycle ordering avoids the cycle; the deleted OTP returns invalid credentials. |
| Deactivation versus reset issuance | Issuance waited at the credential insert, then inserted a reset and delivered mail after deactivation completed. The inactive account retained one reset row. | Issuance waits before active lookup; no pending row or message is produced. |
| Deactivation versus OTP issuance | Issuance returned success and delivered a code after deactivation. The inactive account retained one OTP row. | Issuance rejects the inactive account; no pending row or message is produced. |
| Canceled OTP lookup | A blocked credential query was canceled with `pg_cancel_backend`; original PostgreSQL `57014` was replaced by `commit unexpectedly resulted in rollback`. | Original `57014` and `database.KindUnavailable` survive. Retrying the same OTP succeeds, proving the failed query did not consume it. |

Regression source:

- `internal/adminauth/account_credentials_concurrency_integration_test.go:16`
- `internal/adminauth/account_credentials_concurrency_integration_test.go:100`
- `internal/adminauth/account_credentials_concurrency_integration_test.go:184`

Executed targeted RED/GREEN commands:

```sh
go test -tags=integration ./internal/adminauth -run '^TestAccountPasswordChangeSerializesPendingCredentialRedemption$' -count=1 -v
go test -tags=integration ./internal/adminauth -run '^TestAccountDeactivationPreventsLateCredentialIssuance$' -count=1 -v
go test -tags=integration ./internal/adminauth -run '^TestEmailOTPPropagatesCredentialQueryCancellation$' -count=1 -v
```

After the fixes, the complete administrator auth package passed with race detection and the repository's integration skip guard:

```sh
PGBASE_INTEGRATION_PACKAGES=./internal/adminauth bash scripts/test-go-integration.sh -race -count=1
# PASS
# ok github.com/yakisoba0728/PgBase/internal/adminauth 26.791s
```

Executed in the existing `pgbase-remaining-runner` fixture with its configured PostgreSQL integration URL. Owned Go files were formatted; scoped `git diff --check` passed.

## Bounds

The global lifecycle lock intentionally serializes these short administrator credential transactions. This pass does not claim a throughput benchmark or exhaustive interleaving proof. Static review found no additional cycle in management's lifecycle → actor `FOR SHARE` → target lock sequence: other management mutations enter the same lifecycle lock first; account listing holds only the actor share lock and performs an unlocked inventory read.

Mail delivery happens after commit. An issuance that commits first may have its credential invalidated by a later account mutation before SMTP delivery completes. This change guarantees database serialization and prevents stale active-state insertion behind a completed mutation; it does not cancel mail already in flight. Existing CLI reset semantics beyond its lock ordering are unchanged.
