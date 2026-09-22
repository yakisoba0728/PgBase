# Native authentication administration UI continuation

Date: 2026-09-22. Scope is PgBase's own authentication administration contract. Earlier PocketBase comparison documents remain unchanged.

## Implemented workflows

- `/admin/administrators` is reachable through the administrator navigation and direct browser routing. It lists safe account metadata in 50-item cursor pages, creates another administrator, activates/deactivates another account, replaces a password directly, and revokes sessions. The existing current-profile settings remain available.
- The UI disables self-deactivation; server conflict handling remains authoritative for self/last-active protections and duplicate email. Deactivation and session revocation identify the affected account and request confirmation. Password replacement explicitly explains that every session is revoked and no email is sent.
- Password inputs use password controls, are cleared when a request starts, and are never persisted to browser storage or rendered from account responses. There is no account deletion or bulk mutation UI.
- Successful password replacement or session revocation for the current administrator clears the HTTP adapter's local authentication state and triggers the existing session-provider transition to login. Failed mutations preserve the local session. The account ID and authentication generation guard prevent a late response from expiring a newer login.
- The authentication policy form now exposes the five native fields previously absent from the UI: maximum password bytes, Go password pattern, login identity field names, MFA challenge lifetime, and native MFA rule JSON. It sends the complete existing policy DTO and preserves server-returned values. Numeric fields use browser range validation; the server remains authoritative for Go regular expressions, profile identity eligibility/uniqueness, and MFA rule semantics.
- Policy guidance distinguishes UTF-8 byte lengths, named login identity fields, single-use MFA challenge duration, and the native `{field,equals}` JSON rule. It does not introduce PocketBase filter syntax or token-secret rotation.

## Source boundaries

| Responsibility | Source |
| --- | --- |
| Safe account DTO, typed port and domain error | `ui/admin/src/backend/ports.ts` |
| Native administrator HTTP requests and error translation | `ui/admin/src/backend/http-administrators-port.ts` |
| Current-session identity/generation ownership | `ui/admin/src/backend/http-backend.ts` |
| Administrator account workflow, mutation guard and timeout | `ui/admin/src/pages/administrators-page.tsx` |
| Navigation and direct routes | `ui/admin/src/app.tsx`, `ui/admin/src/routing/admin-location.ts` |
| Full native auth policy form | `ui/admin/src/components/auth-policy-editor.tsx`, `auth-administration-panel.tsx` |

The new runtime page and adapter are each below 800 lines. This work does not change the Go backend or SDK; root owns the corresponding administrator backend endpoints and integration lifecycle.

## Red/green evidence

`auth-policy-http.test.tsx` uses the real HTTP backend and session provider, substituting fetch only at the external HTTP boundary. Its two regressions failed because the maximum-password control did not exist. After implementation they passed, checking all five newly editable policy fields in the exact native PUT body, cookie/CSRF options, and rejection of invalid password bounds before a mutation.

`administrators-http.test.tsx` renders the actual app using its direct route, session provider and HTTP backend. The first six tests failed because the administrator screen/route did not exist. They subsequently passed. Two additional regressions cover a rejected own-session revoke and a successful old-session response arriving after another login. The suite checks safe native create/update/reset request bodies, bounded cursor queries, self-deactivation gating, password clearing, immediate own-session expiry, and preservation of an unrelated current session.

Focused execution:

```text
npm test -- src/pages/administrators-http.test.tsx src/components/auth-policy-http.test.tsx src/pages/auth-collections-page.test.tsx src/routing/admin-location.test.tsx src/backend/http-backend.test.ts
5 files, 124 tests passed
npm run typecheck
passed
```

The existing auth-collection tests were updated only to provide the complete native policy fixture. Existing current-profile and session semantics were not replaced with a second authentication system. Root subsequently executed the complete UI suite: **50 files / 624 tests passed**, along with typecheck and the production build. Root generated the dist assets and rebuilt the embedded native server; this UI agent did not perform a concurrent build.

## Actual browser integration

`ui/admin/e2e/authenticated/continuation-auth-administration.spec.ts` defines two native-server flows:

1. Create a temporary second administrator from the UI, sign in separately, deactivate/reactivate it, replace its password and verify login with the replacement, revoke its sessions from the first account, then self-revoke and verify immediate login navigation. The response assertion rejects unexpected account-response members. Cleanup deactivates the temporary administrator; fixture teardown belongs to root.
2. Create a disposable auth realm with a text username profile field, edit the complete native policy from the UI, check persisted policy through the actual server, reject a malformed Go regular expression, reload the prior valid policy, and delete the disposable realm.

After root's coordinated build and fixture restart, both flows passed against the native server at `http://127.0.0.1:58091`, using installed Chromium headless shell 1234:

```text
ui/admin/node_modules/.bin/playwright.cmd test continuation-auth-administration.spec.ts --config=.local/integration-runtime/playwright.config.ts --project=authenticated-chromium
2 passed (4.6s)
```

Credentials were read from the ignored fixture file into environment variables, without printing their values. The first browser attempt passed the administrator flow but timed out in a test locator for the wrapped identity textarea before any policy save. The locator was changed to its accessible textbox role/name and actions were bounded to 10 seconds; production code was unchanged. The exact timed-out test realm was deleted through the native API (204), and both workflows passed on rerun. The successful policy test deleted its realm, and temporary administrator accounts were left inactive for root's disposable database teardown.

The final production-source follow-up only removed two unnecessary trailing blank lines requested by root. Runtime behavior was unchanged after the successful build and browser run.

## Deliberate limits

- Administrator management grants the existing full administrator authority. It introduces no roles, invitations, account deletion, or editing another administrator's email.
- Password replacement is an authenticated administrator action that directly sets a password and revokes sessions; it is not the public email recovery workflow.
- Policy forms retain the native server's validation and transition semantics. The UI does not reimplement Go regular expressions or evaluate MFA rules in JavaScript.
- Token key rotation and additional OAuth provider metadata are outside this bounded continuation.
