# PocketBase reference research and pgBase capability roadmap

Status: primary-source research snapshot, 2026-08-31 (Asia/Seoul)

## Research question

What must pgBase add, and in what dependency order, to become a PostgreSQL-native backend that preserves the useful PocketBase product model while remaining safe for multi-instance and production operation?

This document is a reference and sequencing aid. It does not make PocketBase's SQLite storage choices normative for pgBase. pgBase keeps PostgreSQL as its durable source of truth and keeps every user-visible mutation inside the existing principal-aware Record Operations transaction seam.

## Pinned upstream snapshot

The reference repository was pinned rather than read as a moving `master` branch:

- PocketBase `master` and tag `v0.40.1`: commit [`bc8ffed4e7265a70a6e8de76c0b0b48b945e19ef`](https://github.com/pocketbase/pocketbase/tree/bc8ffed4e7265a70a6e8de76c0b0b48b945e19ef).
- Relation implementation: [`core/field_relation.go`](https://github.com/pocketbase/pocketbase/blob/bc8ffed4e7265a70a6e8de76c0b0b48b945e19ef/core/field_relation.go).
- Collection lifecycle implementation: [`core/collection_model.go`](https://github.com/pocketbase/pocketbase/blob/bc8ffed4e7265a70a6e8de76c0b0b48b945e19ef/core/collection_model.go).
- Official relation behavior: [Working with relations](https://pocketbase.io/docs/working-with-relations/).
- Official record and transaction API: [Go record operations](https://pocketbase.io/docs/go-records/).
- Official collection and API-rule model: [Collections](https://pocketbase.io/docs/collections/) and [API rules and filters](https://pocketbase.io/docs/api-rules-and-filters/).
- Official files model: [Files handling](https://pocketbase.io/docs/files-handling/).
- Official realtime model: [Realtime API](https://pocketbase.io/docs/api-realtime/).
- Upstream explanation of application-level relation integrity: [PocketBase discussion #565](https://github.com/pocketbase/pocketbase/discussions/565).
- Product deployment boundary: [PocketBase FAQ](https://pocketbase.io/faq/).

## Upstream facts that affect pgBase

### Relation values and validation

PocketBase exposes a relation field as a record id for a single relation and an ordered array of record ids for a multi relation. `MaxSelect > 1` selects multi-value behavior; `Required`, `MinSelect`, and `MaxSelect` constrain cardinality. Server-side validation verifies that all referenced records exist. Multiple values are normalized to unique ids, and append, prepend, and subtract modifiers are available.

Its physical representation is SQLite `TEXT` for single values and JSON for multiple values. It deliberately validates relation integrity in the application instead of declaring SQL foreign keys because its collection target and cardinality are dynamic. That is a PocketBase implementation choice, not a production requirement for pgBase.

### Expand and reverse relations

PocketBase permits comma-separated and dot-path expansion. Nested relation references in expand, filter, and sort are bounded to six levels. Only target records permitted by the target collection's View rule are included. Reverse relations use the `collection_via_field` form, and a reverse expand is capped at 1,000 records.

The behavior establishes useful product expectations:

- relation values remain ordinary JSON fields in CRUD payloads;
- expanded records are returned under a separate `expand` object;
- nested traversal has a hard bound;
- authorization is applied at every target hop;
- reverse traversal is discoverable without adding a physical field to the source record.

The exact depth and count are not compatibility promises for pgBase. pgBase needs lower configurable budgets that are enforceable by a query-cost planner, plus deterministic truncation or rejection semantics.

### Collection and rule model

PocketBase distinguishes base, view, and auth collections and attaches list, view, create, update, and delete API rules to collections. Rules act both as access control and record filters. Ownership is commonly expressed through relation fields, for example comparing a record's user relation with the authenticated record id.

This means relations must land before public authorization rules: the policy language, planner, and record projection all need stable relation metadata and traversal semantics.

### Transaction and event model

PocketBase documents an application transaction API and warns callers to use the transaction-scoped application handle. pgBase already has a stronger PostgreSQL mutation boundary: record data, revision, audit event, outbox event, idempotency state, and optimistic-concurrency checks commit together. Relation mutations and delete effects must extend that transaction rather than introduce a parallel writer.

### Files and realtime

PocketBase models file fields as record fields, supports local or S3-compatible storage, protected delivery, thumbnails, and orphan cleanup. Its realtime API publishes record changes over subscriptions. For pgBase, both features depend on identity/policy and durable event delivery: an upload cannot become visible before its record commit, and a realtime subscriber must not observe a record it cannot view.

### Scaling boundary

PocketBase presents itself as a single-server product that scales vertically. pgBase's stated target includes multiple application instances over one PostgreSQL database and production-grade operators. Consequently, pgBase must intentionally add database-enforced integrity, lock ordering, leases/fencing, bounded query plans, operational evidence, and multi-instance tests instead of reproducing single-process assumptions.

## Current pgBase baseline

The repository already has three deep Modules:

1. Schema lifecycle: deterministic plan/apply, DDL and catalog metadata in one transaction, schema versioning, advisory locking, and `NOTIFY` invalidation.
2. Record Operations: principal-aware reads and mutations, schema pinning, ETags, create idempotency, ordered record locks, revision rows, audit rows, and transactional outbox rows.
3. Operational telemetry: bounded metrics, protected exposition, PostgreSQL pool visibility, migration/schema/auth/readiness outcomes, dashboards, and alerts.

The present schema catalog supports scalar PostgreSQL fields only. The query compiler assumes every field maps to a column. There is no relation catalog, junction storage, relation-aware digest, expand planner, reverse traversal, dependency-aware collection deletion, or relation-aware doctor check.

## Capability gap matrix

| Capability | PocketBase product behavior | pgBase state | Required pgBase seam |
|---|---|---|---|
| Single relation | id-valued field with target existence validation | Missing | Schema catalog + UUID FK + Record Operations validation |
| Multi relation | ordered unique id array with min/max | Missing | Junction table + relation mutation executor |
| Self/cyclic relation | Dynamic application-level reference | Missing | Dependency graph and deterministic lock ordering |
| Delete behavior | Configurable cascade-like behavior | Missing | Record Operations delete graph; never raw database cascade |
| Collection deletion | Protects referenced collection lifecycle | Missing | Schema dependency guard under schema lock |
| Expand | Dot paths, target rule filtering, bounded depth | Missing | Relation traversal compiler + policy port + cost budget |
| Reverse relation | `collection_via_field` virtual traversal | Missing | Catalog-derived back-reference descriptor |
| Public auth | Multiple auth collections and auth flows | Missing | Public identity/session Module using Record Operations |
| API rules | Collection CRUD rules and filters | Missing | Typed policy AST/compiler bound to query and mutation seams |
| API credentials | Superuser/admin and auth records | Administrator session only | Scoped service accounts/tokens and role model |
| OpenAPI/SDK | Documented REST surface and first-party SDKs | Missing | Versioned public API contract and deterministic generator |
| Files | Record-bound file fields and object storage | Missing | Blob metadata/state machine + storage Adapter + GC |
| Durable delivery | Realtime record subscriptions | Outbox writer only | Lease/fencing dispatcher, retry, DLQ, checkpoints |
| Policy-aware realtime | Subscribers see permitted records | Missing | Policy projection at subscription and delivery time |
| Advanced query | Relation filter/sort/expand | Scalar bounded filter/sort only | Costed relation query planner and batch loader |
| Operations | Single-server-oriented guidance | Kernel telemetry and deployment artifacts exist | Staging, load, restore, failure, and release evidence |
| Control plane | Per-instance application | One project per database | Project registry, tenant isolation, quotas, lifecycle APIs |

## Required dependency order

### Lane A — release and operational evidence (parallel throughout)

- Reproduce the complete test and image build from a clean checkout.
- Exercise TLS/load balancer, external PostgreSQL, secrets, rollout, and rollback in staging.
- Run load, soak, spike, database-failure, and network-partition tests.
- Complete backup/PITR restore drills and record RPO/RTO.
- Establish signed image, registry, vulnerability scan, incident, and migration-compatibility evidence.

This lane is operational evidence and should not block design work, but no large-company production claim is valid without it.

### Lane B — relation and schema lifecycle

1. Add stable relation metadata and deterministic physical-name derivation.
2. Plan relation DDL and metadata atomically with collection DDL.
3. Add database-enforced target integrity without allowing database cascades to bypass Record Operations.
4. Add single and ordered-multi relation mutation inside Record Operations.
5. Add collection-delete dependency checks and bounded record-delete effects.
6. Add batched forward/reverse expansion with explicit budgets.
7. Add relation-aware filter/sort only through the same planner.
8. Extend doctor, telemetry, admin API/UI, OpenAPI fixtures, and E2E coverage.

### Lane C — public identity and policy

1. Versioned public route surface and credential taxonomy.
2. Auth-collection schema and password/session/verification/reset flows.
3. Typed rule AST and compiler with fail-closed evaluation.
4. Collection CRUD policy enforcement in both query and mutation Modules.
5. Relation-hop authorization and projection tests.

### Lane D — durable delivery and realtime

1. Outbox claim protocol with lease, fencing token, heartbeat, retry schedule, and dead-letter state.
2. At-least-once dispatcher and idempotent consumer contract.
3. Subscriber registry and cursor/checkpoint protocol.
4. Policy-aware projection and revocation behavior.
5. Multi-instance reconnect, replay, partition, and slow-consumer tests.

### Lane E — files

1. File-field metadata and staged/uploaded/committed/deleting state machine.
2. Local and S3-compatible storage ports and Adapters.
3. Content limits, sniffing, names, checksums, signed/protected reads, and thumbnail jobs.
4. Commit-after-upload and garbage-collection reconciliation.
5. Backup/restore and lifecycle evidence.

### Lane F — public contract and control plane

- Generate deterministic OpenAPI and SDK artifacts from the versioned API contract.
- Add compatibility tests before supporting more clients.
- Later add project registry, tenant isolation, quotas, per-project secrets, migrations, backups, placement, and fleet operations.

## Relation slice: PostgreSQL-specific design constraints

### Storage

- A single relation should use a nullable or `NOT NULL` UUID column and a deterministic index.
- A multi relation should use a deterministic junction table with `(source_id, target_id, position)`, uniqueness for `(source_id, target_id)` and `(source_id, position)`, and an index beginning with `target_id` for reverse lookup.
- Physical target foreign keys should use `ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`. This remains a commit-time guard against bypassing the record kernel while allowing a required relation cycle to be deleted completely inside one transaction. Database `CASCADE` or `SET NULL` would mutate user-visible records without revision, audit, outbox, ETag, or policy processing; PostgreSQL `RESTRICT` is also unsuitable because its check cannot be deferred.
- The application-level delete policy may be `restrict`, `cascade`, or `set_null`, but its entire bounded graph executes through Record Operations in one transaction and produces normal artifacts for every changed record.

### Metadata and schema lifecycle

- Relation metadata must reference stable collection and field ids, not mutable names.
- Cardinality, minimum, maximum, target collection, delete policy, and storage version participate in the schema digest and plan checksum.
- Changing the target collection or cardinality is a destructive migration, not an in-place flag edit. The first release should require remove-and-add or an explicit future migration workflow.
- Collection deletion must fail while any incoming relation metadata remains. The operator must first remove or migrate those fields.
- Self-relations and cycles are legal only if planning, deletion, lock ordering, and traversal budgets handle them deterministically.

### Mutation

- JSON remains ergonomic: a single relation accepts one UUID or `null`, and a multi relation accepts an ordered UUID array.
- Duplicate multi ids are rejected rather than silently reordered. Cardinality and target existence are checked in the transaction snapshot.
- Relation rows and base record changes commit with revision, audit, outbox, and idempotency state.
- All target/source record locks use a stable order such as `(collection_id, record_id)` after the schema catalog is pinned.
- Error text never leaks SQL, table names, hidden records, or whether a policy-inaccessible target exists.

### Read and expand

- The default record response exposes raw relation ids. Expansion is opt-in and returned under `expand`.
- Expansion input is parsed to a typed path tree before SQL generation.
- Enforce maximum depth, maximum paths, maximum target rows per relation, total expanded rows, total response bytes, and planner complexity.
- Fetch in batches per hop; do not issue one query per parent record.
- A policy port filters target records at every hop. Until public policies exist, only the administrator policy Adapter is installed, but the seam must already be present.
- Reverse relations are virtual catalog descriptors and never writable fields.
- Cycles terminate by depth and cost; identity-based deduplication may reduce work but must not change response shape.

## Invariants that later Modules may not bypass

1. PostgreSQL is the only durable source of truth.
2. Schema DDL, relation metadata, schema version, and invalidation commit together.
3. User-visible mutation, relation edges, revision, audit, outbox, idempotency, and optimistic concurrency commit together.
4. No database referential action may create an unaudited user-visible mutation.
5. Every relation read and write is authorized through a replaceable policy Interface; the current administrator Adapter is allow-only for a validated administrator principal.
6. Query/expand work has static and runtime budgets with stable rejection codes.
7. Multi-instance behavior relies on PostgreSQL locks, constraints, and leases, never process-local ownership.
8. Schema and relation identifiers are quoted and derived only from validated catalog metadata.

## Recommended next vertical slice

Implement relation/schema lifecycle before public auth, files, or realtime:

1. relation metadata migration and catalog types;
2. deterministic schema planning for single and multi storage;
3. single and multi CRUD inside Record Operations;
4. dependency-safe delete behavior;
5. one-hop batched administrator expand;
6. doctor, telemetry, API/UI, migration, concurrency, and PostgreSQL E2E tests;
7. then deepen to nested/reverse/filter/sort expansion under cost and policy ports.

This slice has the highest leverage because identity ownership rules, file ownership, and policy-aware realtime all need a trustworthy relation graph. It also exposes the hardest integrity and lock-order problems while the public surface is still administrator-only.

## Explicit non-goals for the first relation release

- Automatic online conversion between scalar, single relation, and multi relation.
- Cross-database or cross-project relations.
- Unbounded recursive expansion or arbitrary SQL joins supplied by callers.
- Database-triggered user-visible cascade or set-null behavior.
- Compatibility with PocketBase's SQLite physical layout.
- Public-rule syntax, anonymous access, file storage, or realtime delivery in the same change set.

## Evidence required before calling the relation slice complete

- Empty and populated migration upgrade, repeatability, and old-checksum preservation.
- Deterministic plan/checksum tests for forward, self, cyclic, and incoming dependencies.
- PostgreSQL constraint and rollback tests for single and multi edges.
- Concurrent create/update/delete tests from separate pools, including opposite lock-order attempts.
- Exact audit/revision/outbox/idempotency evidence for relation changes and delete effects.
- Bounded expand tests for depth, paths, rows, bytes, cycles, hidden targets, and no-N+1 query count.
- Collection delete refusal while referenced and successful removal after explicit relation deletion.
- Doctor detection of missing columns, constraints, junction tables, indexes, or metadata.
- Admin API/UI and Docker E2E coverage, plus full race, vet, frontend, migration, and security checks.
