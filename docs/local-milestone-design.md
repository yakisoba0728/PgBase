# M2–M6 local implementation boundaries

The user explicitly expanded the product beyond the completed R11 refactor and M1/M2-A authentication scope. The new modules implement email actions, OAuth/TOTP and auth collections, typed action rules, service tokens, files, realtime, hooks, atomic batches, jobs, compound indexes and source-backed views. This feature addition establishes measured structural ceilings rather than pretending the old refactor budget can contain the new product surfaces. Existing rationale and the no-production-file-over-800-lines constraint are retained.

The data group remains database + query + recordops + recordaccess + relation + schema. Typed rules belong to a separate bounded compiler; relation policy binding stays in the read transaction and record writes enforce permissions before transaction commit. New schema objects are additive system migrations 12–21; migrations 1–11 remain unchanged.

Structural budgets are enforced by repository tests and should be read from those executable checks rather than copied into this design note. The TypeScript SDK remains outside the historical admin-UI accounting set; its mock and actual HTTP conformance gates cover it. No dead-code deletion or comment compression is used to pay for these features.

New service interfaces are optional application dependencies. SMTP, OAuth and storage remain explicit configuration. Files expose streaming object operations with lifecycle state in PostgreSQL. Realtime sends metadata with durable delivery identity and bounded replay; it does not turn best-effort Go hooks into guaranteed external effects. Jobs are trusted Go callbacks with cooperative cancellation and fencing. Service tokens carry explicit collection/action scopes; user and anonymous access uses typed row rules and current collection admission.

Views project typed fields from one base collection with a static typed filter. They do not accept SQL text, aggregation, nested views, relation targets or mutations. Their data access uses source collection admission and source rules, including fields omitted from the projection. View schema changes require recreation; source field changes that would invalidate a view are rejected.

Local tests, restore exercises, cross-builds and the short load run are recorded in [local verification](local-verification.md). They do not establish production cloud readiness. Embedded JavaScript and Dart SDK scope is documented in [extensions and client scope](extensions-and-client-scope.md).

## Final scope accounting (2026-09-09)

The staged source report against `09d6b7d` measures 41,256 Go runtime lines and 4,376 admin UI lines (45,632 combined), with gross runtime additions/deletions of +8,344/−248. The data group is 18,913 lines: database 468, query 5,439, recordops 2,692, recordaccess 359, relation 1,999, schema 7,956. These measured values are the new zero-slack ceilings for this explicitly expanded product scope. Production Go has 34 interfaces, 653 exported declarations and 37 constructors; UI has 60 direct page state/effect calls and 142 exports. No production file exceeds 800 lines. The separately measured SDK source is 634 lines; it is outside the historical runtime category and is covered by SDK/package/browser conformance. Reproduce with `go run ./tools/repostats --compare 09d6b7d --pretty` after staging new files.
