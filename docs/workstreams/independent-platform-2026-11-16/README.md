# Independent PostgreSQL platform workstream

This work continues Draft PR [#148](https://github.com/yakisoba0728/PgBase/pull/148) on `refactor/independent-platform-20261116`. The baseline is main `8f551de4423a6b64d9c0ac94fff9b9bd07264f2a`; system schema remains **67**. The continuation delivered concrete security, SDK lifetime, query allocation, test and evidence corrections. The wider whole-platform audit and sustained production qualification remain open; this is not a claim that every module was redesigned or certified.

## Read the current evidence

| Document | Purpose |
| --- | --- |
| [Validation](validation.md) | Exact runtime revisions, actual local/CI results, initial failures, independent reviews and remaining scope |
| [Security review](security-review.md) | Original CodeQL 27 Go / 3 JavaScript findings, all-result classification, reproduced defects and analysis-scope correction |
| [Performance](performance.md) | Paired 500000-row profiles, mixed HTTP load, execution plans, allocation reductions **and latency regressions**, raw measurements and reproduction |
| [Architecture and migration](architecture-and-migration.md) | Authentication generations, query ownership/byte accounting, mail rendering, module coverage and compatibility guidance |
| [SDK cookie contract](../../sdk-auth-cookies.md) | `pgbase_auth` defaults, explicit legacy names and TS/Dart cookie semantics |
| [Historical handoff](validation-handoff-history.md) | Previous-session record retained separately; it is not the current verification result |

Final runtime [`5e4b71d1`](https://github.com/yakisoba0728/PgBase/commit/5e4b71d1f602c9c136809bf65f2375bf75fc4a9f) completed **all 15 jobs** in [full qualification 43780150632](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632). Full-scope CodeQL reports **24 Go / one JavaScript finding**, individually reconciled; the final image retains **32 lower-severity/unknown dependency records**. Neither is represented as an empty security inventory.

## Implemented continuation

| Area | Concrete change and boundary |
| --- | --- |
| Security evidence | Recovered and digest-verified the original SARIF ZIPs and job logs; corrected the zero-finding report and broken extension-rule metadata lookup. Require full-scope source analysis, retaining rejected diff-only evidence. |
| Archive extraction | Check each tar entry against remaining expanded-size budget before addition; reject the reproduced overflow before reading its body and clean staging files. Existing traversal/inventory controls remain. |
| Authentication | Accept exactly six ASCII TOTP digits; preserve leading zeroes and reject the reproduced signed alias. Actual password storage remains Argon2id. |
| Mail | Separate trusted administrator markup from dynamic strings through contextual HTML templates; reject malformed contexts and test SMTP header/DATA boundaries. |
| Browser example | Remove query-selected credential destinations; close/cancel SSE resources on every tested failure path. |
| TypeScript and Dart | Guard obsolete administrator/public results, errors, uploads, final authentication adoption and Dart persistence across shared-store changes. Preserve caller cancellation and current-session failures. |
| Query allocation | Borrow ordered catalog fields read-only, preallocate scan capacity and reuse scalar-row byte measurements only when no hydration occurs. Preserve post-hydration serialization and response limits. |
| Administrator UI | Preserve visual layout and styling; exercise actual text-rendering boundaries and repair a pagination-reset test fixture using the real observer/reset lifecycle. |
| Runtime dependencies/data | Update the unused-OpenPGP module inventory and pin refreshed public CA/timezone data with matching metadata; capture actual final-image assets separately from scanner package claims. |
| Verification | Real agents in separate worktrees, failing-before regressions, paired load/profiles, complete opt-in CI, clean SDK/UI generated-output reproduction and masked disposable E2E fixture credentials. |

Earlier PR work on gzip negotiation, compiled CIDR quotas, pgx 5.13.0, shared image limits, `pgbase_auth` cookies and independent product terminology remains in place. This continuation checked their source/qualification context and focuses its new changes on reproducible defects and measured costs.

## Design and operating boundaries

PostgreSQL owns durable state. One project can use multiple application instances sharing the database, consistent secrets/configuration and appropriate shared file storage. The continuation preserves the existing record/revision/audit/outbox transaction model; it introduces no sharding, replica routing, distributed cache, schema migration or weaker authorization policy.

Authentication results belong to an operation lifetime. Data already delivered to application code cannot be revoked, and a locally rejected mutation may already have committed. Application caches/subscriptions must follow identity changes; mutation retries must use the existing idempotency/version contract.

Measurements are scoped evidence. The paired native profile reduced a 50-row page from about 1.12 MB to 0.52 MB allocated per operation, but some p99 latencies regressed. The mixed HTTP fixture also showed worse p99 in every measured stage. These results do not establish production capacity or a general speedup.

## Remaining work

- Sustained end-to-end mixed workloads with realistic application relation graphs, workload-specific SLOs, network/storage faults, large restores and measured distributed realtime slow-client behavior.
- Exhaustive module-by-module security/architecture review, adversarial transaction/deadlock schedules and custom extension/logger boundaries beyond the reviewed paths.
- Broad keyboard/screen-reader, cross-browser and large-table UX assessment; external OAuth/SMTP/provider verification.
- Remaining image dependency findings, including unfixed/source-conflicting records, as detailed in the security review. A successful severity gate is not an empty inventory.
- Repository About metadata and any decision to retire legacy adapters; required legal notices and historical attribution remain intact.

Three actual independent agents and a working local Go/Dart/PostgreSQL toolchain were used in this continuation. CI jobs are not counted as independent reviewers. All development data and fault tests used disposable fixtures. No main push/merge, operational database access, production deployment, tag, package publication or release promotion was performed. The PR remains a draft.
