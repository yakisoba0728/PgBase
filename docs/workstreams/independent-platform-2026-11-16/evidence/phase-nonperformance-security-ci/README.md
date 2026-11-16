# Bounded final security CI evidence

This directory supports [the security CI report](../../phase-nonperformance-security-ci.md).
`manifest.json` records SHA-256 for every included evidence file and decoded
job-log bytes. Download redirect URLs and credentials are not retained.

| Directory/file | Meaning |
| --- | --- |
| `ci-43836410887/` | Fresh final-source Go/JS, image, runtime-assets and SBOM artifacts; exact GitHub metadata; three final security-job logs; parsed comparison |
| `ci-43834531061/` | Previous source attempt with successful security jobs, retained separately from the later doctor test correction |
| `ci-43833754019/` | Fresh artifacts/logs from the earlier source attempt, retained as historical evidence; bounded zlib download diagnostic and initial browser failure excerpts |
| `baseline-43820007603/` | Previously reviewed baseline ZIP bytes copied from existing evidence and matched to their recorded artifact digests; not freshly scanned/downloaded |
| `../phase-nonperformance-doctor-qa/` | Independent exact-source doctor schema-69 regression, one top-level/12 subtests, and its provenance |
| `static-correction/` | Independent PostgreSQL 17.9 UTF8/race execution of the two rewritten cleanup regressions, fixture start/stop logs and exact tree-equivalence provenance |
| `reconcile.py` | Reads stored artifacts and Git objects to compare findings, extraction, source blobs, package/trust/timezone inventory and artifact digests; does not run scanners |

From the repository root, with the recorded source/merge objects available:

```sh
python3 docs/workstreams/independent-platform-2026-11-16/evidence/phase-nonperformance-security-ci/reconcile.py \
  . \
  docs/workstreams/independent-platform-2026-11-16/evidence/phase-nonperformance-security-ci/baseline-43820007603 \
  docs/workstreams/independent-platform-2026-11-16/evidence/phase-nonperformance-security-ci/ci-43836410887 \
  56a881f47d0c27877b91959d7df641c2f603ef03 \
  0a517baea58bf65e2293f15fb0a9fb54321f7eb7 \
  43836410887
```

The baseline identity key is `(ruleId, message, partialFingerprints)`, counted
as a multiset. The final comparison resolves rule references and primary source
URI/region for each identity, including duplicate identities. It reports Go
result reordering and table reindexing explicitly; it does not claim exhaustive
code-flow or raw-byte equality. Original ZIPs remain intact. Image comparison
checks the entire results array and rule metadata after parsing. Earlier recheck
JSON files retain the comparison format used for their original attempts.
An unchanged finding identity is not itself proof of safety; the report binds
the comparison to the existing source classifications and retained limitations.

The source SARIF summaries retain the scanner's original `unreviewed` label.
No result suppression, scan-scope reduction or performance execution occurs in
this reconciliation. Other workflow jobs are deliberately outside this evidence
collection and belong in the coordinator's final qualification report.
