# Administrator account management

2026-09-22 continuation of the independent PgBase product work. These are local implementation and fixture results, not a deployment record.

## Native contract

`internal/adminauth/accounts.go` adds authenticated, bounded listing, account creation, activation/deactivation, direct password replacement and session revocation. `internal/api/admin_accounts.go` exposes the five routes documented in [public-api.md](../../public-api.md#administrator-accounts). The management UI is recorded in [continuation-ui.md](continuation-ui.md).

All active administrators retain the same authority. This is not a new delegated-role system. Account responses contain only ID, email, active state and timestamps. Creation does not issue a new session. Other-account email editing and HTTP deletion are outside this feature; existing self-profile and recovery CLI remain available.

Mutations share the existing CLI lifecycle advisory lock and recheck the actor's active state in the transaction. Concurrent administrators cannot deactivate each other and leave no active administrator. Self-deactivation is rejected. Password changes preserve inactive state, TOTP and IP policy. Password replacement and deactivation atomically invalidate sessions, pending reset links and email OTPs; session revocation alone removes sessions. A request authenticated before revocation may already be in flight: the new transaction checks active account state, not a fresh session token.

## Evidence

- The new real HTTP management test first returned 404 before route implementation, then passed after implementation (0.407s). It exercises safe creation output, two-page listing, duplicate email, strict payloads, self-deactivation protection, inactive password replacement, reactivation, session revocation, authentication and CSRF rejection.
- `accounts_integration_test.go` runs two service instances against real PostgreSQL and observes advisory-lock contention for simultaneous mutual deactivation. Exactly one succeeds, one gets invalid credentials, and one administrator stays active.
- That suite also rejects all six management operations from an inactive actor; proves that password/deactivation remove target credentials without touching actor credentials; verifies TOTP/IP policy and rejection of old credentials after reactivation. Four tests/eight subcases passed with `-race` in 3.890s.
- Independent review found a lock-order inversion between management changes (administrator then pending credential) and reset/OTP consumption (pending credential then administrator). Deterministic PostgreSQL regressions reproduced both cycles and late credential issuance behind deactivation. Consumption, issuance and CLI reset now enter the same lifecycle order; issuance rechecks active state inside the write transaction. An additional cancellation regression verifies the original database error survives OTP completion. The full administrator package passed with `-race` and the integration skip guard in 26.791s. See [concurrency evidence and mail-delivery bounds](administrator-credential-concurrency.md).
- The OpenAPI route generator now uses the actual administrator service capability. It captures all five new routes and the four pre-existing self-profile routes previously omitted by its minimal fixture.

The generated UI, full package regression results and exact source measurements are recorded in [continuation-validation.md](continuation-validation.md) after final verification.
