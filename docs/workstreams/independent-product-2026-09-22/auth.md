# Native authentication integrity — 2026-09-22

PgBase's own authentication contracts are the target. PocketBase compatibility is not a requirement for this work. The comparison audit under docs/audits/2026-09-22-pocketbase-comparison remains unchanged.

## Confirmed failures and regression evidence

Tests use the public userauth.Service transaction boundary against PostgreSQL 17.9 in pgbase-remaining-runner, Go 1.26.8, with the existing per-test database fixture. Email delivery and OAuth provider exchange are replaced only at their external boundaries.

| Regression | Before the fix | After the fix |
| --- | --- | --- |
| TestVerificationRejectsTokenSentBeforeEmailChange | ConfirmVerification returned nil for a token mailed to the previous address after an administrator changed only email. | Returns ErrInvalidCredentials; the replacement address stays unverified; a fresh token for it succeeds. |
| TestEmailOTPRejectsCodeSentBeforeEmailChange | Both service-key and collection-key cases authenticated with a code sent to the previous address. | Both reject the old code and accept a fresh code sent to the replacement address. |
| TestPublicRegistrationEnforcesCollectionCreatePolicy | OAuth ignored a false create rule and a required custom field; password signup also ignored the required field. All three failing subtests returned nil. | Denied signup leaves no user; after allowing the policy, the same OAuth state can be retried successfully. |

Red runs preceded production edits for each failure group. Initial failing commands:

```text
docker exec -w /workspace pgbase-remaining-runner go test -p 2 -tags=integration ./internal/userauth -run '^TestVerificationRejectsTokenSentBeforeEmailChange$' -count=1
docker exec -w /workspace pgbase-remaining-runner go test -p 2 -tags=integration ./internal/userauth -run '^TestEmailOTPRejectsCodeSentBeforeEmailChange$' -count=1
docker exec -w /workspace pgbase-remaining-runner go test -p 2 -tags=integration ./internal/userauth -run '^TestPublicRegistrationEnforcesCollectionCreatePolicy$' -count=1
```

Additional regression coverage checks delegated manager email changes, verify/password-reset/email-change action tokens with collection keys, administrator token lookup, unchanged valid passwords, policy changes committed during an in-flight OAuth exchange, generated required autodate profile fields, and the actual administrator HTTP → public confirmation flow with local SMTP.

## Native design

Email actions now bind their existing credential_hash to the action purpose, password state, and source email address captured at issuance. Confirmation reads the current password and email while holding the action/user row locks before applying any mutation. The administrator token lookup uses the same binding. Email-change payload_email continues to identify the destination; the credential binding independently identifies the source account state.

Email OTP hashes include the normalized delivery address as well as their challenge ID. Redemption uses the currently locked user's email, for both the service key and configured collection keys. This changes which account state an email credential can authorize; it does not change the native OTP verification policy.

The binding describes the current email value, not a permanent credential epoch. If an administrator changes A to B and later restores exactly A before expiry, an otherwise valid pending credential can match again. This follows from static inspection of the hash inputs; the regression runs below cover A to B, not that round trip. Permanent invalidation after every reassignment is a separate policy and is not claimed by this change.

No database migration or plaintext email-token storage was added. The new versioned HMAC domains deliberately reject pending tokens/codes issued by the old version, since those rows do not contain the original email needed to validate them safely. After an upgrade, users with an outstanding verification/reset/email-change message or login OTP must request a fresh message. Passwords, ordinary sessions, and OAuth links are not invalidated solely by this deployment. A mixed old/new process rollout cannot enforce the new binding consistently; update authentication-serving processes together before issuing fresh email credentials.

Public password and OAuth signup both run completePublicRegistration inside their existing transaction, before committing a session or OAuth identity. It locks the current collection policy, checks registration admission and nullable createRule, creates and validates generated profile fields, and evaluates createRule against the candidate record with an anonymous principal and the supplied email request value. Rejection rolls back the inserted user/profile/public-ID reservation. Existing linked OAuth users and explicit account-link flows are not treated as new signup.

The current signup API accepts email/password or provider identity, not arbitrary custom profile input. Therefore a required custom field without a generated value rejects public signup; this patch does not silently fill such a field or introduce a new signup DTO. Privileged typed auth-record creation can still supply the required profile data.

## Intentional policies retained

- TOTP enrollment/recovery remains the native MFA model.
- OAuth linking remains explicit and password-bound; no automatic email-based account merge was added.
- Email OTP does not silently mark an email verified.
- Verification does not randomize passwords, delete OAuth links, or revoke valid sessions merely because verified changed.
- Auth rules, administrator trust, email visibility, and record ID policies retain their existing interfaces. This work does not claim to resolve every comparison-audit row.

AUTH-36's PocketBase cleanup behavior was assessed as a policy difference, not copied. The implemented changes address confirmed PgBase address/credential and collection-admission integrity failures.

## Files changed

Production:

- internal/userauth/email_actions.go — email/password/purpose credential binding and locked confirmation.
- internal/userauth/email_otp.go — challenge/delivery-address binding.
- internal/userauth/auth_auxiliary.go — consistent administrator token lookup.
- internal/userauth/registration_policy.go — common transaction-bound public signup policy.
- internal/userauth/service.go — password signup uses the common policy.
- internal/userauth/oauth.go — new OAuth signup uses the common policy.

Regression tests:

- internal/userauth/email_identity_binding_integration_test.go
- internal/userauth/registration_policy_integration_test.go
- pgbase/email_identity_binding_integration_test.go

All production files remain below the 800-line budget; the largest changed file is service.go at 688 lines. No schema, UI, query, migration, or historical audit files were edited.

## Final checks

Passed on the actual PostgreSQL fixture:

```text
docker exec -w /workspace pgbase-remaining-runner go test -p 2 -tags=integration ./internal/userauth -count=1
ok github.com/yakisoba0728/PgBase/internal/userauth 13.667s

docker exec -w /workspace pgbase-remaining-runner go test -p 2 -race -tags=integration ./internal/userauth -count=1
ok github.com/yakisoba0728/PgBase/internal/userauth 27.364s

docker exec -w /workspace pgbase-remaining-runner go test -p 2 -race -tags=integration ./pgbase -run '^(TestVerificationHTTPRejectsTokenAfterAdministratorEmailChange|TestEmailActionsThroughHTTPAndLocalSMTP|TestAuthCollectionHTTPIsolationAndProfile|TestJavaScriptExactCatalogAndAuthFamiliesUsePostgreSQL|TestAuthCollectionHTTPPreservesNullAndEmptyRules)$' -count=1
ok github.com/yakisoba0728/PgBase/pgbase 4.631s
```

The full userauth race run includes all new Service regressions. The selected pgbase race run verifies the new HTTP/local-SMTP regression plus existing email flows, realm isolation, nullable-rule HTTP behavior, and JavaScript administrator token helpers. Formatting and git diff whitespace checks passed. No Docker restart or commit was performed.
