# M2-A: administrator user management and password changes

Date: 2026-09-08. Baseline: M1 / `52af13d96bf72fcd03845da396197c68eb10926b`.

M2-A makes the existing project-user model manageable through the administrator API/UI and adds current-password changes for public users. It reuses migration 11 without changing existing migration SQL or checksums. Email recovery, Auth collections and arbitrary record policies remain separate roadmap work.

`internal/userauth` owns the lifecycle operations alongside authentication. Administrator deactivation and explicit session revocation lock the user row before updating sessions. Login's existing shared user lock orders session creation against these operations. A deactivated account cannot log in; reactivation does not un-revoke old sessions. Revocation alone permits a subsequent fresh login. These operations preserve record ownership and data.

Password changes bound input, credential/KDF concurrency and shared account failures. Verification and hashing occur outside the transaction's user lock. The transaction rechecks the exact old hash and active state, updates the password and revokes all sessions atomically. Tests pause credential verification to force competing changes; only one succeeds. Failure or cancellation does not report uncommitted revocation counts.

The API reuses the existing user-service capability and adds four declared routes. Administrator reads and mutations retain cookie, same-origin and CSRF admission; the password route requires public-user Bearer authentication. Explicit response DTOs exclude hashes and tokens. User pages are bounded to 100 rows and advance by UUID; they are live pages rather than a frozen snapshot.

The administrator UI adds one Users page and collection policy controls. A synchronous pending guard prevents overlapping row actions before React renders disabled controls. Request and component-lifetime guards prevent old list/mutation completions from changing a fresh page. Policy controls account for both incoming and outgoing relations and offer a retry after initial lookup failure. The HTTP adapter uses existing CSRF and administrator-session generation behavior, covered by dedicated wire-level tests.

## Measured scope

`go run ./tools/repostats --compare 52af13d96bf72fcd03845da396197c68eb10926b` records +666/−10 runtime lines, net +656. This adds a new product surface and records its measured baseline without introducing growth headroom or deleting explanatory comments.

| Metric | M1 | M2-A |
| --- | ---: | ---: |
| Handwritten runtime | 36,840 | 37,496 |
| Go runtime | 33,173 | 33,590 |
| UI runtime | 3,667 | 3,906 |
| Production interfaces | 20 | 20 |
| Exported Go declarations | 482 | 488 |
| Constructors | 27 | 27 |
| Page state/effect occurrences | 19 | 30 |
| UI runtime exports | 119 | 123 |
| Production files over 800 lines | 0 | 0 |

The additional UI state handles the Users page's data/loading/pending/error/success states and collection policy loading/update states. The data-group ceiling and Go interface/constructor/file-size ceilings remain unchanged. User-facing behavior and limitations are documented in [user management](user-management.md); execution evidence and follow-up work are tracked in [TODO](../TODO.md).
