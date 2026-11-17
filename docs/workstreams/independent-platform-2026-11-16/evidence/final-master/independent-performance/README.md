# Independent final performance evidence

## Scope and result

This is an offline audit of the single final qualification run, not another workload execution. The final gate **FAILS**: scheduled-to-validated p99 is **588.031143 ms**, exceeding the unchanged **250 ms** limit. The parser succeeds on provenance, raw population, timings, durability and resource-budget checks; it deliberately exits **1** because the gate and the Go test failed. Its 44/46 checks are not a replacement for the full qualification matrix's job or step counts.

The candidate is `89e883fc700ef0250024427a1f09bb88affe0383`. Actual CI checkout `b5c17b6f989bb11575e83544d37cc37fc6385d19` has the same tree, `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55`. Review files were prepared on a separate documentation worktree; they are not represented as part of the executed candidate.

## Evidence map

| File | Purpose |
| --- | --- |
| `manifest.json` | Input/output hashes, immutable source identifiers and canonical archive locations |
| `artifact-metadata.json` | Fresh artifact API metadata supplied by the root reviewer |
| `verify_final_performance.py`, `final-verification.json` | ZIP identity/CRC, complete raw population, five quantile series, log/source correspondence, diagnostics and durability |
| `summarize_final_resources.py`, `final-resources.json` | Load-aligned host CPU, whole-observer container extrema, process counters and connection accounting |
| `compare_load_windows.py`, `window-comparison.json` | Historical/final ten-second arrival windows, sampled waits, pool occupancy and cumulative WAL/I/O deltas |
| `phase-and-source-comparison.json` | Setup/load/post-load timing and exact-source comparison |
| `executed-data-assertions.json` | Narrow manual assertion-execution conclusion, separate from failed whole-test acceptance |
| `verify_historical_retention.py`, `historical-failure-retention.json` | Independent verification that recovered original failure bytes and 547 drops remain preserved |

Canonical fresh bytes are retained in [final-mixed-http.zip](../final-ci/artifacts/final-mixed-http.zip), with the [complete job log](../final-ci/logs/job-133701568842.log.gz), [final job metadata](../final-ci/jobs.json) and [source provenance](../final-ci/source-provenance.json). The eight ZIP members include the complete 120,000-row CSV and 6,000-sample database trace. Extracted copies used for analysis were temporary scratch files and are not duplicated in Git.

The historical pass is [pgbase-final-mixed-http.zip](../../final-integrated/performance/final-gate/pgbase-final-mixed-http.zip), executed for source `4091c3be20ec8db177886a2a85550924fce37ff5`. It remains historical evidence. The recovered [historical failure archive](../historical-failed-gate/original-artifact.zip) and [metadata](../historical-failed-gate/metadata.json) preserve run `43820007603`, artifact `14204951142`, including its original failed test output.

## Measurement and inference limits

- End-to-end time starts at each intended arrival and ends after response validation. HTTP service starts at the worker attempt. Worker queuing and generator delay are independently retained. Quantiles cannot be added across distributions.
- The fixed offered-load interval was 600 seconds; measured elapsed including final drain was 600.000612834 seconds. The 764.53-second whole test includes about 161.117 seconds before the scheduled load and 3.413 seconds after it. UTC job timestamps have small log-delivery delays.
- Host CPU means use 600 one-second interval samples aligned to the load. The initial since-boot vmstat average is excluded. These are host-wide observations, not server CPU.
- The Go process includes the server, HTTP client, generator and diagnostics. Its allocation/heap and runtime CPU counters cannot be assigned exclusively to the server. **Application RSS and peak RSS were not recorded.** Heap allocation is not RSS.
- Docker output contains 3,070 display frames including redraws, 3,062 with metrics and eight unavailable frames. PostgreSQL peak displayed memory was approximately 382,520,524 bytes (364.8 MiB), peak displayed CPU 149.17% and peak PIDs 20. Docker MemUsage is container memory accounting, not application RSS. Without timestamps, these extrema span setup/load/drain and cannot be precisely assigned to the load; frame counts do not imply independent time samples or a time average.
- The request pool stays capped at four. Seven persistent diagnostic connections comprise four pool connections, listener, backup gate and optional observer. Transient maintenance connections are extra; active storage operations can independently require up to 64 dedicated coordination connections per controller. This workload does not exercise that storage worst case. Maintenance attribution relies on explicit query prefixes because the sampler omits backend type.
- WAL and pool observations localize coincident delay; sampled wait events are not duration measurements. Ten-second request buckets use intended arrivals, so requests may span their bucket boundaries. Cumulative counters and host intervals have different sampling scopes.
- Historical and final hosts differ (Intel Xeon Platinum 8573C versus AMD EPYC 9V74). Both report four logical CPUs, Microsoft virtualization, Go 1.26.11 and PostgreSQL 17.9. This is not a controlled same-host comparison or a speed claim. Increased WAL-sync time, WALWrite waits and queuing do not prove a sole hardware cause, a specific source regression or a correction that would justify retrying.
- All three load harnesses, qualification workflow, module dependencies and query runtime are unchanged versus the historical pass. The changed Go production file is the storage lease ownership guard; this text/integer/relation workload performs no file operations. Embedded UI changes prevent claiming the complete binary is identical.
- The actual database observer recorded all 6,000 scheduled observations without a diagnostic error. An earlier `resource observer failed` message belongs to the expected negative subprocess unit test, which passed its six checks. Bounded observer shutdown is not an actual load-observer failure.
- Physical SQL totals and complete owner traversal occur before the sole reported latency assertion. This supports the narrower executed-assertions conclusion in `executed-data-assertions.json`; no raw database dump independently recounts the persisted rows. The generic integrity verdict stays UNPROVEN because its conservative automatic rule requires a whole-test PASS.
- Git-retained ZIPs preserve raw bytes after Actions expiry (fresh: 2026-12-01; old failure: 2026-11-30). Original API hashes and per-member hashes remain in the evidence. The old failure did not capture every durability setting, including `full_page_writes`; absent metadata stays unknown and is not interpreted as disabled durability.

No benchmark, server, scanner, runtime test or qualification rerun was started in this independent phase. No threshold, runtime or workflow was changed. Release acceptance remains BLOCKED.

## Offline reproduction

Run from a checkout containing the evidence. Point `PGBASE_SOURCE` at a clean checkout of the exact candidate with the synthetic checkout Git object available. The following only extracts and parses retained files. The main verifier is expected to return **1** and save a FAIL JSON. The remaining helpers return **0**; those are successful analyses, not passing qualification results.

```bash
PGBASE_EVIDENCE=docs/workstreams/independent-platform-2026-11-16/evidence
PGBASE_AUDIT="$PGBASE_EVIDENCE/final-master/independent-performance"
PGBASE_SOURCE=/path/to/clean-frozen-candidate
PGBASE_RAW=$(mktemp -d)
PGBASE_RESULTS=$(mktemp -d)
PGBASE_ZIP="$PGBASE_EVIDENCE/final-master/final-ci/artifacts/final-mixed-http.zip"
python3 -m zipfile -e "$PGBASE_ZIP" "$PGBASE_RAW"
gzip -dc "$PGBASE_EVIDENCE/final-master/final-ci/logs/job-133701568842.log.gz" > "$PGBASE_RAW/job-133701568842.log"
python3 "$PGBASE_AUDIT/verify_final_performance.py" \
  "$PGBASE_RAW" "$PGBASE_ZIP" "$PGBASE_AUDIT/artifact-metadata.json" \
  "$PGBASE_RAW/job-133701568842.log" "$PGBASE_SOURCE" "$PGBASE_RESULTS/final-verification.json" \
  --candidate 89e883fc700ef0250024427a1f09bb88affe0383 \
  --tree c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55 \
  --checkout b5c17b6f989bb11575e83544d37cc37fc6385d19 \
  --run-id 43982303611 --job-id 133701568842 --artifact-id 14283838362
python3 "$PGBASE_AUDIT/summarize_final_resources.py" \
  "$PGBASE_RAW" "$PGBASE_RESULTS/final-verification.json" "$PGBASE_RESULTS/final-resources.json"
python3 "$PGBASE_AUDIT/compare_load_windows.py" \
  "$PGBASE_EVIDENCE/final-integrated/performance/final-gate/pgbase-final-mixed-http.zip" \
  "$PGBASE_ZIP" "$PGBASE_RESULTS/window-comparison.json"
python3 "$PGBASE_AUDIT/verify_historical_retention.py" \
  "$PGBASE_EVIDENCE/final-master/historical-failed-gate/original-artifact.zip" \
  "$PGBASE_EVIDENCE/final-master/historical-failed-gate/metadata.json" \
  "$PGBASE_RESULTS/historical-failure-retention.json"
```

Keep reproduction outputs outside the input extraction directory when adding extra analyses: the verifier hashes every extracted regular file for traceability. Absolute scratch paths in saved outputs identify the original review execution; canonical ZIP hashes and relative repository paths identify portable retained evidence.
