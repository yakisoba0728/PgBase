# Native signup profiles — 2026-09-22

Public password and new-account OAuth signup can now satisfy a collection's required custom profile fields. This extends PgBase's native admission contract; it does not merge provider metadata automatically or copy another product's account model. The historical comparison audit is unchanged.

## Wire contract

These existing POST endpoints accept an optional `fields` JSON object:

- `/api/v1/auth/register`: `{ "email": "member@example.test", "password": "...", "fields": { "display_name": "Ada" } }`.
- `/api/v1/auth/{authCollection}/register`: the same body for a selected auth collection.
- `/api/v1/auth/oauth/complete`: `{ "provider": "local", "state": "...", "code": "...", "fields": { "display_name": "Ada" } }`; the collection remains bound by the previously issued OAuth state.

The existing `{ token, user, expiresAt }` session response is unchanged. A caller reads the profile through the existing collection profile endpoint, whose response is `{ "attributes": { ... } }`. The query-string `fields` response projection remains independent from body `fields` input. Login, OAuth start/link, MFA, and OTP inputs do not accept profile input.

Omission and an empty object preserve existing signup behavior, including rejection when required fields have no generated value. Explicit null, arrays, and scalars are invalid requests. The existing Go `Register`, `RegisterInCollection`, and `CompleteOAuth` signatures remain wrappers; their new `WithFields` variants accept `map[string]json.RawMessage`. Existing HTTP dependency implementations can retain the original interfaces; a nonempty profile fails closed if an implementation does not implement the additive capability.

OAuth profile input applies only when creating a new user. Nonempty fields for an existing linked identity or explicit account-link flow return a validation error before identity/session mutation or state consumption. They do not update or merge the existing profile. OAuth provider exchange itself still precedes transaction-bound schema/policy validation: a rejected attempt leaves PgBase state unconsumed, but a real provider may already have consumed its one-use authorization code. Such a caller may need to restart authorization; fixture retry coverage does not promise provider-code reuse.

## Admission and integrity

Password and new OAuth signup share `completePublicRegistration` and `prepareRegistrationProfile` inside the account/session transaction. They lock the collection definition, check current registration/create policy, validate typed input and constraints, generate server dates, resolve public relation IDs, validate target/file ownership, hash custom password fields, check identity-field uniqueness, persist the candidate, and evaluate its create rule. Any failure rolls back the account, profile, ID reservation, and new OAuth identity.

Unknown names, system/security names (including id, email, password, verified, active, token keys and ownership identifiers), and caller-supplied autodates are rejected. This also rejects a reserved name when a custom schema happens to declare it. Normal custom hidden/password fields retain their existing typed validation and write-only password behavior. Provider verification determines a new OAuth user's verified state; profile input cannot set it. Password signup remains unverified and active.

Rule request data retains the actual `email` and nested `fields` shape, with custom password values redacted. No flat aliases are added. `@request.body.fields:isset` tests presence, and bare schema field references such as `display_name = 'Allowed'` evaluate the persisted candidate. The current shared rule resolver does not support nested `@request.body.fields.display_name` value selectors; this change does not claim to add that grammar. A collection requiring a file that cannot yet belong to the new user still rejects signup under the existing ownership contract.

JSON numbers are decoded with `UseNumber`, checked against PostgreSQL limits, and preserved through profile storage, reads, and unrelated patch writes. Bigint requires an integral signed 64-bit value; integer keeps its signed 32-bit boundary. Geopoint validation accepts decoded JSON numbers. Numeric comparisons for existing `:changed` checks compare values exactly so switching stored decoding to `json.Number` does not mark an unchanged legacy float input as changed.

## Evidence

The initial PostgreSQL HTTP regression sent body `fields` to a collection registration route and received 400 `invalid_request` instead of 201 (0.220s), before production changes. The fixture's create rule was then narrowed to supported `fields:isset` plus candidate-field syntax, and its existing profile response envelope was corrected; these were test-fixture corrections, not newly added rule grammar.

Focused integration checks passed:

- Public password HTTP signup with required fields, generated autodate, exact numeric/JSON values, then profile GET: 0.532s.
- Password/OAuth matrix for required/type/constraint failures, unknown/security/readonly fields, integer/JSON limits, denied create policy, rollback/retry, custom password hashing, exact read/patch values, and existing OAuth profile rejection: 0.810s.
- Explicit OAuth link injection rejection without identity creation/state consumption, relation target validation, and custom identity uniqueness: 0.675s.
- Default and collection password HTTP signup, response projection, malformed bodies, and OAuth methods → start → completion with profile and policy denial: 1.246s.

Final checks on Go 1.26.8 and the existing PostgreSQL 17.9 fixture:

```text
docker exec -w /workspace pgbase-remaining-runner go test -p 2 -tags=integration ./internal/userauth -count=1
ok github.com/yakisoba0728/PgBase/internal/userauth 16.294s

docker exec -w /workspace pgbase-remaining-runner go test -p 2 -race -tags=integration ./internal/userauth -count=1
ok github.com/yakisoba0728/PgBase/internal/userauth 29.276s

docker exec -w /workspace pgbase-remaining-runner go test -p 2 -race ./internal/api -count=1
ok github.com/yakisoba0728/PgBase/internal/api 19.799s

docker exec -w /workspace pgbase-remaining-runner go test -p 2 -race -tags=integration ./pgbase -run '^(TestPublicPasswordSignupAcceptsTypedProfileHTTP|TestDefaultSignupProfileInputAndMalformedBodiesHTTP|TestOAuthSignupAcceptsProfileThroughMethodsEndpointHTTP|TestPublicOAuthPKCEStateExchangeSessionAndReplayHTTP|TestAuthCollectionHTTPIsolationAndProfile|TestAuthCollectionHTTPPreservesNullAndEmptyRules|TestVerificationHTTPRejectsTokenAfterAdministratorEmailChange)$' -count=1
ok github.com/yakisoba0728/PgBase/pgbase 5.605s
```

The full race run includes the new signup matrix, explicit-link and relation/identity regressions, and prior email-credential/policy lifecycle regressions. The selected HTTP race run also exercises existing PKCE/replay, realm isolation, nullable rules, and stale verification behavior. Formatting and whitespace checks passed. The largest changed production file is `internal/userauth/service.go` at 697 lines. No admin-account endpoints, migrations, SDK/UI source, historical audit files, Docker restarts, or commits were changed by this subtask.

## Composition regression follow-up

The root's full pgbase integration run found that the additive `WithFields` methods were promoted through `hookedUserAuth`'s embedded service, bypassing its existing lifecycle overrides. This affected requests even when profile input was omitted because the HTTP capability assertion selected the promoted method. The existing `TestAuthLifecycleNamedRealmVetoAndNondisclosingRequests` reproduced the failure independently with PostgreSQL and `-race` (0.671s): a vetoed named registration succeeded.

An additional actual-HTTP matrix reproduced the bypass before the wrapper fix (0.993s). It checks default password signup, named password signup, and OAuth completion with omitted fields, `{}`, and a nonempty profile. Veto must run before delegation, its paired After callback must run once, and no account/session/OAuth identity may persist. Successful profile signups also check exactly one Before/After pair and the session/realm result projection. Profile values and credentials are not added to the secret-free `AuthEvent`.

Explicit `RegisterWithFields`, `RegisterInCollectionWithFields`, and `CompleteOAuthWithFields` overrides now delegate through the existing lifecycle chain. Legacy methods forward to these wrappers, so both surfaces run the same hooks once. Production changes are limited to `pgbase/auth_lifecycle_hooks.go` and `pgbase/auth_oauth_lifecycle_hooks.go`; tests are in `pgbase/signup_hooks_integration_test.go` and the expanded `pgbase/auth_lifecycle_hooks_test.go`.

```text
docker exec -w /workspace pgbase-remaining-runner go test -p 2 -race -tags=integration ./pgbase -run '^(TestAuthLifecycleNamedRealmVetoAndNondisclosingRequests|TestSignupProfileHooksWrapHTTPInputs|TestEveryAuthLifecycleVetoPreventsDelegationAndPairsAfter|TestPublicPasswordSignupAcceptsTypedProfileHTTP|TestDefaultSignupProfileInputAndMalformedBodiesHTTP|TestOAuthSignupAcceptsProfileThroughMethodsEndpointHTTP)$' -count=1
ok github.com/yakisoba0728/PgBase/pgbase 3.995s
```

The root is responsible for rerunning the complete pgbase integration suite on this corrected composition. The focused pass above is not reported as a full-suite pass.
