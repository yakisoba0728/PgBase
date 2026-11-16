# Independent final performance evidence review

**PASS — 41/41 offline checks.** No benchmark, server, database fixture or Go test was rerun.

The freshly downloaded GitHub artifact 14224442513 from run [43861850923](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923), job [133280386571](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/job/133280386571), has SHA-256 `82f0397e6b0ab5e1d71c96ebc2c4af35137fc8638d60c3c262b8208d5935d84e`. Its 3,528,654 bytes match both GitHub's digest and the previously preserved original ZIP. GitHub expiry is **2026-11-30 12:08:39 UTC / 21:08:39 KST**. Preserve the existing original instead of duplicating it.

Every CSV identifier 0–119999 occurs exactly once; all 120,000 requests were attempted and completed. Missing, duplicate, malformed, dropped, unscheduled and unexpected-error counts are zero. Workload, owners, expected HTTP statuses, 5 ms scheduling and latency-field arithmetic agree. Independent nearest-rank calculations reproduce all five logged p50/p95/p99 sets, including end-to-end p99 **22.221162 ms** and HTTP p99 **21.098702 ms**. The unchanged 200 RPS / 600-second / 250 ms gate passes.

The exact checkout's passing Go test includes unconditional post-drain physical SQL counts (24,256 rows; 12,000 creates and 12,000 updates), complete per-owner traversal (12,128 rows each), ETag, relation, capped totals/cursor and cross-owner 404 assertions. This is executed-assertion evidence, not an independent physical database dump.

All 6,000 observer samples retain pool maximum 4. Before/after settings agree: fsync, synchronous_commit and full_page_writes are ON. Seven persistent diagnostic sessions comprise four requests, listener, backup gate and optional observer; transient maintenance sessions are additional.

Source files match checkout `ba838872d72993abf1953dcafc64ba043f978c06`, tree `653a46758f9e6af9662b77ecfe2f069a2ac94cb4`, also candidate `4091c3be20ec8db177886a2a85550924fce37ff5`. Schema 69, complete CI and listener QA are covered separately by Agent B. Historical 547-drop evidence remains a failure; no causal or universal performance claim is made.

## Reproduction

Extract the preserved original archive to `raw/`, then run the provided offline verifier with paths to its metadata, job log and copied source files:

```sh
python verify_existing_performance.py raw pgbase-final-mixed-http.zip artifact-metadata.json mixed-http-job.log source performance-independent-verification.json
```

The verifier reads existing evidence only. Detailed counts, hashes, source provenance, settings and limitations are in the adjacent JSON files.
