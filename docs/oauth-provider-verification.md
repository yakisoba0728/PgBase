# OAuth provider verification

Verified on 2026-11-14 using Go 1.26.11 and disposable PostgreSQL 17 fixtures. These checks distinguish local provider contracts, live public metadata, and an authorized login against an external provider.

## Controlled provider contracts

`TestSupportedOAuthProviderHTTPContracts` exchanges authorization codes through local HTTP servers for all 29 built-in profile normalizers, including both Microsoft Graph and OIDC response shapes. Apple is covered separately by signed-token and database-bound callback-bridge tests. The tests exercise state/PKCE request parameters, token credentials, JSON/form bodies, bearer authentication, provider-specific headers, Linear/monday GraphQL requests, GitHub/Gitea/Bitbucket email retrieval, numeric subjects, and withholding untrusted email claims. Fixtures reject accidental external requests.

Existing OAuth integration tests cover persisted realm/state binding, callback replay rejection, registration policy changes during exchange, signup profiles, linking, unlinking, independent identities without email, and session issuance. API callback tests also exercise Apple form callbacks and administrator client-secret generation. These are controlled-provider tests; they do not establish compatibility with every current live provider deployment or consent policy.

The additional HTTP tests reproduced and corrected these protocol mismatches:

- GitHub's token endpoint defaults to a form response. JSON consumers now request `Accept: application/json`. [GitHub authorization documentation](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps).
- Notion token exchange uses JSON with Basic client authentication; profile requests include the pinned `Notion-Version: 2026-03-11`. [Notion authorization documentation](https://developers.notion.com/guides/authorization/overview), [required API version header](https://developers.notion.com/reference/versioning).
- The built-in Microsoft catalog uses OIDC UserInfo, whose subject is `sub`, while custom Graph presets use `id`. Both shapes now supply a subject. Graph email behavior is preserved; an OIDC email claim without verification evidence is not marked verified. [Microsoft UserInfo response](https://learn.microsoft.com/en-us/entra/identity-platform/userinfo).

## Live public metadata

Unauthenticated HTTPS discovery and JWKS requests returned HTTP 200 with nonempty public key sets for Google, Apple, Microsoft, GitLab, and Gitea. The observed authorization/token endpoints match their corresponding presets or server catalog. Google/GitLab/Gitea advertised S256 where their discovery documents included PKCE methods; absence of that field for Apple/Microsoft is not a PKCE verdict. Microsoft advertises a tenant-specific issuer template, so a generic OIDC configuration must use its actual tenant issuer rather than treating `common` as a literal issuer.

Primary discovery endpoints:

- [Google](https://accounts.google.com/.well-known/openid-configuration)
- [Apple](https://appleid.apple.com/.well-known/openid-configuration)
- [Microsoft](https://login.microsoftonline.com/common/v2.0/.well-known/openid-configuration)
- [GitLab](https://gitlab.com/.well-known/openid-configuration)
- [Gitea](https://gitea.com/.well-known/openid-configuration)

These requests verify public metadata reachability and basic configuration alignment. They do not exercise authorization, consent, token redemption, user profile permissions, provider account policies, or account creation.

## External authorized login unavailable

No OAuth test-application credentials were supplied, and the user confirmed that no external OAuth test application/account is available. No personal account was used, no consent was granted, and no real external authorization code or access token was requested. External authorized-login verification therefore remains **not performed**, not passed.

A future authorized check needs a provider-owned test application, its client ID and supported secret/key material, a registered callback URI matching the deployed PgBase URL, required scopes, and a dedicated consenting test account. Apple additionally needs a Services ID/domain configuration, public HTTPS callback bridge, and a valid signed client-secret JWT or its team ID/key ID/signing key inputs. Self-hosted OAuth/OIDC needs the actual issuer/endpoint configuration and an isolated test identity. Credentials should be supplied through a secret manager or local configuration, never committed.

Detailed command results, intentional before-fix failures, credential-name inventory, and public metadata observations are recorded under `.local/redesign/runtime-verification/oauth-*` in the verification workspace. No actual external OAuth end-to-end pass is claimed.
