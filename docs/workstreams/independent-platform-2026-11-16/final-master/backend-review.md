# Final-master backend review

## Source and outcome

Reviewer: `/root/backend_quality`, separate backend worktree. The incoming
source is `242f80aea730ee483cc22798fb6450bdb3064081`. The bounded review found
one reproducible P2 storage-routing defect and produced correction
`e59fc07668e0a282555f2f52dbddf890162254e0`, tree
`63b3b6eb3379e32967b16d5a637e9aa1d1052f95`.

This is an implementation-and-local-QA report. A different reviewer and the
coordinator's exact-source CI must qualify the integrated result. It does not
declare the complete PR ready or equate a historical scan with a fresh scan.

## Adopted correction: storage lease ownership

`StorageController.BeginStorageOperation` and `OpenRange` reuse a context lease
only when that lease belongs to the receiving controller. `Put`, `Open`, and
`Delete` omitted the same check, so a context from controller A directed calls
on controller B to A's object store. `GuardStoragePublication` likewise lacked
an explicit controller ownership check. The latter could accept a different
controller's live coordination session and matching database revision.

The correction adds four ownership conditions. A foreign context now acquires
the target controller's own lease for object I/O, and a foreign lease cannot
authorize metadata publication. Own-controller lease reuse is unchanged.
There is no schema, public API, storage format, migration, or visual change.

The unit regression reproduces all three I/O failures before the correction:
the canceled target admission is bypassed; a put replaces the foreign object;
a delete removes it; an open succeeds through the foreign store. The full
filestore unit/race suite passes after correction. The prepared PostgreSQL regression
uses two real controllers, distinct local namespaces, a shared disposable
database and a real first-controller lease. It checks reads, writes, deletion,
publication ownership, and release of the target controller's temporary lease.

**Classification:** P2 service/context ownership correctness. The demonstrated
precondition is trusted/composed calls carrying another controller's context.
No public HTTP exploit, cross-tenant account compromise, or production data
loss is claimed. The internal storage controller is not a new user-facing
capability. Reverting the single correction commit restores the previous
behavior; no database downgrade is needed.

## Executed verification

The exact commands, exits, timestamps, source-file hashes and raw evidence are
in [the backend manifest](../evidence/final-master/backend/manifest.json).
Complete new JSONL streams are compressed with gzip after byte-for-byte
round-trip verification; their original hashes and lengths remain in the
manifest. Historical raw evidence was not altered.

| Execution | Result | Scope |
| --- | --- | --- |
| Baseline race/unit run | 13 packages, 360 top-level tests passed; zero failures/skips | accessrule, adminauth, userauth, backup, cursor, filestore, jobs, lifecycle, recordaccess, recordops, realtime, schema, database |
| New ownership regression on old runtime | Expected failure in put/open/delete | Three unit subtests reproduce wrong-store reuse |
| Corrected filestore race/unit suite | 38 top-level tests passed; zero failures/skips | Includes new ownership regression and all untagged filestore tests |
| PostgreSQL ownership and retained high-risk contracts | NOT RUN by this author | Explicitly delegated to independent QA after disposable PostgreSQL provisioning |

The coordinator assigned PostgreSQL execution to a separate reviewer so this
author can release the review slot without duplicating that work. The prepared
integration test has not been executed here; passing unit tests do not prove
its database assertions. The final independent-QA report owns that outcome.

Go is the repository-pinned 1.26.11. The baseline run discovered its tests before
the new regression files were added, and its output does not include the new
ownership test. Production files stayed unchanged throughout that baseline
execution. The corrected race run used the working-tree production/test bytes
later committed as `e59fc076`; its manifest preserves their hashes instead of
pretending the commit existed before execution.

## Bounded source review

The following paths and invariants were actually inspected. These are selected
review boundaries, not assertions that every behavior in each module was
exhaustively audited.

| Boundary | Inspected result and limitation |
| --- | --- |
| HTTP/auth/embedded request rules | `accessrule/request_context.go`, `api/request_rule_context.go`, `userauth/auth_rule_resolver.go`, `recordaccess/rule_compile.go`: HTTP metadata remains adapter-owned, actor/body values come from service state, and transport-dependent rules fail closed without request metadata. Arbitrary expression combinations were not exhaustively enumerated. |
| Transaction/panic outcome | `database/tx.go`, `recordops/hooks.go`, mutation entry-point defers, `lifecycle/registry.go`, `pgbase/trusted_database.go`, `extension_app_transaction.go`: bounded rollback/connection cleanup and failure notification preserve caller panics; external effects are not rolled back by these helpers. |
| Durable variants and schema 69 | `filestore/{variants,staging,object_gc,restore,storage_migration,storage_settings}.go`, `000069_durable_variant_cleanup.sql`: intent precedes Put, reference/intent publication is transactional, uncertain writes retain tombstones, legacy DELETE is guarded, retained uncertainty blocks namespace activation, schema-68 restore checks column availability. Remote quiescence still requires operator proof. |
| Jobs | `jobs/{service,management}.go`: one claim per sequential pass; accepted edits revoke token/generation; completion requires live lease; schedule replacement resets occurrence while an unchanged schedule retains retry identity. External side effects still require application idempotency/fencing. |
| Listener | `schema/listener.go`: hijacked initialized session preserves request-pool capacity; reload uses consistent catalog transaction, writer lease refresh, cancellation-aware retry and bounded close. |
| OIDC/archive | Current OIDC numeric-time/key-purpose admission and archive total-expanded/trailing-byte bounds were compared to retained regression contracts. Existing avatar-policy regressions also run in the userauth suite; this is not a new exhaustive address-policy audit. Real OAuth/SMTP providers and actual cloud-object fault semantics are outside this local execution. |

## Existing scanner findings and retained restrictions

[The raw-baseline recheck](../evidence/final-master/backend/security-baseline-recheck.json)
parses and hashes the retained Go, JS and image ZIPs from historical run
`43861850923`. It confirms 25 source results with raw severities **1 CRITICAL /
17 HIGH / 7 MEDIUM**, all reported-flow source blobs equal to the historical
tested source, and 32 all-severity image findings. It does not represent a new
CodeQL invocation, image build, dependency audit, or proof that every finding
is harmless. The raw scanner classification remains `unreviewed`.

Narrow source checks reaffirmed the critical mail path's CR/LF rejection and
SMTP DATA writer boundary; the reported XSS digest helpers' concrete SHA-256/
HMAC writers; the password comparison helper's distinction from Argon2id
password storage; bounded page allocation; both halves of cursor catalog-version
comparison; and the archive's fresh private extraction/path restrictions.
Other previous per-location dispositions remain historical evidence and are
linked by the canonical security report; they were not silently reclassified.
Native PostgreSQL helpers still ship libc/OpenSSL dependencies. Their complete
reachability, upstream/distribution disagreements and architecture limits remain
open operational restrictions. No suppression or warning deletion was added.

## Connection-budget clarification

The four request-pool slots, one dedicated Listener, one backup gate and optional
diagnostic observer are a **persistent diagnostic baseline**, not a universal
`max_connections` upper bound. `storage_settings.go:28` allows 64 storage
operations per controller; line 82 opens an extra coordination session and line
108 closes it on release. `database/pool.go:185–189` explicitly describes these
sessions as outside the request pool. Active storage operations and maintenance
therefore require additional connection headroom.

No speculative optimization or broad refactor was adopted. The adopted change
addresses observed wrong-controller routing; no throughput, allocation or
memory improvement is claimed for it.
