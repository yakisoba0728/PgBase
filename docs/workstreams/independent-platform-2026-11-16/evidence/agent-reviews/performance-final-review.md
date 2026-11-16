# Backend / Performance Agent — final cross-review

Review date: 2026-11-16 UTC. Reviewer: the actual Backend / Performance subagent, using a separate worktree. This report covers the current workstream README, validation, security review, architecture/migration and performance documents, plus the Security Agent's runtime-data changes. The reviewer authored the query optimization and measurements; independent review of those runtime changes was performed by the Security Agent, not claimed as self-independent review here.

The integrated runtime is local `25e0b428c0be30ce808bf51dc2fc4c3db7521be8`, tree `4599bc705d4c4ec440e273cc6db3397f3edcc11d`, mapped by root to published `5e4b71d1f602c9c136809bf65f2375bf75fc4a9f`. Final CI was still pending during this review. No new broad test suite, Docker build or final-image qualification was performed by this reviewer for this documentation pass.

## Findings and corrections

- Found one source-attribution overstatement in `validation.md`: the original 8,000-request HTTP pair exercised filter/sort queries but did not explicitly assert exact rank/title order or per-page uniqueness. Root corrected the paragraph during review. Those stronger assertions belong to the subsequent 1,280-request smoke and the separately recorded 4,000-request combined CI fixture. Original timing/allocation results retain their original harness revision.
- Reported a portability issue in the copied dependency review: references to sibling `xcrypto-*` raw evidence did not resolve in the repository at the time of review. Root was asked to include the compact evidence or identify its local-only status. Original dependency commit `e1939c0721dc938ce7218816ac1df971db0b6f99` corresponds to integrated `c27b883945aceb54deefede73d8463440054e1d5` and published `54c6011a4cf68de7443a299c7624bf076d2b1575`.
- Found no additional blocking allocation, hydration, migration or runtime-trust claim in the five reviewed documents. The deliberately pending final-CI section remains pending evidence, not a pass.

## Performance evidence checks executed

| Check | Result |
| --- | --- |
| SHA-256 of every locked performance artifact | All 25 match the committed manifest; none modified |
| Recorded baseline/candidate runtime and codec benchmark source hashes | All 10 match their recorded original Git revisions |
| Current integrated query/cursor runtime and codec benchmark bytes | Match the measured candidate hashes |
| Native performance table | All 12 rows match machine-readable measurements, allowing displayed decimal rounding |
| Mixed HTTP performance table | All four rows match machine-readable measurements |
| Sample/error totals | 24,000 native queries and 8,000 paired HTTP requests; zero recorded errors |
| Adverse latency observations | Every paired HTTP stage has higher p99; native regressions remain explicitly reported |

The reported 1,116,841 → 517,981 B/op change is a 53.6% reduction for the measured 50-row native page at concurrency one. It is not an HTTP throughput or production-capacity improvement. The original source/harness mappings remain distinct from the combined CI runtime. The whole-process sampled CPU/allocation totals include setup and warmup, exclude PostgreSQL CPU, and are not peak RSS. The fixed-order, single-pair experiment, small mixed fixture, client/harness allocation contribution, separate SQL-plan diagnostics and component-only realtime benchmark are correctly bounded in the documents.

The architecture wording now matches the implementation: scan destinations have length three and capacity `len(fields)+3`; ordered catalog fields are borrowed read-only; scalar byte counts are reused only without a hydration callback; projection remains a later unchanged stage. The NaN regression establishes serialization after hydration, while existing assembler tests separately cover byte budgets.

## Independent runtime-data review and checks

Reviewed Security Agent commits `63f7de9e9b7ce03ee9be0e88a834b9981cb47507` and `0a5298b3a1e3623360719ce3ca0f1f87d4e0ff81` read-only. No blocker found.

- Executed `sh -n deployments/runtime-data.sh`: passed.
- Independently matched both downloaded package SHA-256 values to the recorded signed-index provenance, checked archive `Package`, `Version` and `Architecture`, and checked generated runtime package-status fields. Both pinned packages match: CA `20250419~deb12u2` and timezone `2026f-0+deb12u1`, architecture `all`.
- Independently verified the generated bundle contains 148 PEM certificates and has SHA-256 `e9c7b63ee2623386e9b0a3ee32f499d01a8ea23dc83b8383ddff9f8e749543b7`.
- Inspected hash-before-extraction, isolated helper configuration/certificate/local/hook paths, absence of package-maintainer-script execution, and final overlay ordering after the PostgreSQL client library closures.
- Traced backup subprocess environment filtering: it removes `PG*` variables and retains `SSL_CERT_FILE`. The configured bundle therefore reaches native libpq's system-trust path without disabling certificate/hostname verification.

The Security Agent's exact-library trust probe and Go trust/timezone probes were reviewed as that agent's evidence; they were not independently rerun here. The old/new certificate comparison correctly names the original pinned distroless base. It is not a comparison with the previously assembled final image, whose builder-provided CA bundle was not extracted. Final Docker inventory, actual image execution and TLS integration remain separate CI evidence. Residual native libc/OpenSSL inventory is not declared absent merely because the Go server is CGO-disabled or the HIGH/CRITICAL gate passes.

## Dependency and remaining boundaries

The x/crypto migration note correctly describes an unused OpenPGP package removal, not a demonstrated reachable PgBase vulnerability fix. The previously executed official-tag/checksum, module-selection, source-equivalence and affected race/integration checks are recorded in the dependency review. Used Argon2/blake2b/ACME source is unchanged; no password-format, Go runtime-floor or performance change is claimed. Downstream applications independently importing the removed OpenPGP package must address their own import when module selection chooses 0.59.

This bounded review does not establish sustained production capacity, exhaustive platform security, arbitrary custom-extension safety, large-restore performance or end-to-end distributed realtime capacity. No production resource, main branch, release or package publication was touched.
