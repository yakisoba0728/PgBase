# M1: project users and owner-only record access

Date: 2026-09-08. Baseline: v0.1.0 / `3677a6b182b440a158e96d10482b1552030c18f5`.

## Product boundary

M1 lets a client register, log in and manage its own scalar records without an administrator session. It is the first implementation milestone from [TODO](../TODO.md), not full PocketBase compatibility. The project-wide user model deliberately precedes configurable Auth collections and general access rules.

The administrator must enable each collection. Existing records receive no owner during migration. A user cannot set, transfer or remove ownership. A collection with an incoming or outgoing relation is unavailable through the user API until every relation traversal and delete propagation can enforce per-user rules. The administrator's existing relation operations continue to work.

## Module contracts

- `internal/userauth` owns normalized users, Argon2id credentials, opaque sessions and shared rate limits. Tokens are hashed with the installation secret before persistence. Admission bounds credential, password and session work independently. Account and network login failure buckets update atomically in a deterministic advisory-lock order.
- `internal/recordaccess` owns collection opt-in and active-user authorization in the caller's transaction. The query and mutation modules own catalog validation and ownership predicates; the policy service does not duplicate the schema catalog.
- `internal/query` applies ownership before pagination, binds cursors to the user and hides foreign records. `internal/recordops` commits the owner with the record, revision, audit, outbox and idempotency state. Replay rechecks current authorization.
- `internal/api` admits public credential routes, Bearer session routes and administrator policy routes separately. The application supplies the two new service contracts together. Missing or ambiguous credentials never fall back to administrator cookies.

Policy and active-user rows are held `FOR SHARE` throughout a user operation. Policy disable or user deactivation waits for in-flight operations, then blocks subsequent ones. Reads and mutations also pin the schema through `schema_state FOR SHARE`; schema apply holds `FOR UPDATE` before relation DDL. Relation changes therefore cannot interleave with an operation's catalog decision. Independent review checked both lock paths; database tests exercise the policy/deactivation wait and commit boundaries.

## Migration and restore

Migrations 10 and 11 are append-only; checksums 1–9 remain unchanged. They add users, sessions, shared limits, collection access and record ownership, and extend audit/idempotency actor constraints. The current source requires the complete 11-migration history. Historical release-phase sources and their generation-2 migration behavior remain fixed.

Upgrading a v0.1.0 database requires stopping the old fleet and applying the bulk `migrate up` operation. The one-pending-migration `--through` mode is not a 9→11 upgrade path. See [upgrades](upgrades.md). The restore drill preserves users and owner rows and validates authenticated owner/foreign-user behavior on PostgreSQL 16, 17 and 18.

## Measured scope expansion

The previous R11 budget constrained a refactor of the administrator product. M1 explicitly adds public identity and policy behavior, so this PR records a new measured product baseline rather than presenting the feature as a size-neutral refactor. No UI, generated output, comment-removal or file-size exception pays for this change.

`go run ./tools/repostats --compare 3677a6b182b440a158e96d10482b1552030c18f5` measures tracked working files:

| Metric | v0.1.0 measurement | M1 measurement |
| --- | ---: | ---: |
| Handwritten runtime | 35,822 | 36,840 |
| Go runtime | 32,155 | 33,173 |
| UI runtime | 3,667 | 3,667 |
| Production interfaces | 17 | 20 |
| Exported declarations | 462 | 482 |
| Constructors | 25 | 27 |
| Production files over 800 lines | 0 | 0 |

The runtime comparison category is +1,030 / −20 lines, net +1,010. Eight additional migration-embed lines are categorized as support by the comparison but included in the Go runtime metric, so total measured runtime growth is +1,018. The three interfaces are the password adapter and two HTTP service contracts. The two constructors instantiate the user-authentication and record-access services. The data group grows from its measured 16,714 (previous ceiling 16,747) to 16,888: original packages +100/−3, plus all 77 lines of `recordaccess`. Including that package prevents moving policy code outside the group's accounting. The ceilings are set to these measurements with no growth allowance; the existing UI and 800-line file gates stay in force.

## Verification responsibilities

Unit/API tests check admission, configuration, error classification and module wiring. PostgreSQL tests check actual user/session lifecycle, account/network limiting, default denial, SQL pagination, cursor separation, idempotency, rollback and relation blocking. A real HTTP application test registers two users and exercises isolated CRUD and session revocation. Upgrade and restore tests cover durable state. The current execution results and PR status are maintained in [TODO](../TODO.md).
