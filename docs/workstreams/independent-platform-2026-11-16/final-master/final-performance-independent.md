# Final independent performance review

**BLOCKED.** The frozen candidate fails the unchanged 250 ms end-to-end p99 gate. Final qualification run **43982303611** finished with **14 successful jobs and one failure, mixed-http**. This review performed only offline evidence analysis; no runtime workload or retry was started.

## Identity and fixed gate

| Identifier | Verified value |
| --- | --- |
| Published source | `89e883fc700ef0250024427a1f09bb88affe0383` |
| Executed synthetic checkout | `b5c17b6f989bb11575e83544d37cc37fc6385d19` |
| Shared tree | `c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55` |
| Job / artifact | `133701568842` / `14283838362` |
| ZIP SHA256 | `560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393` |

The [raw verification](../evidence/final-master/independent-performance/final-verification.json) confirms **120,000** unique ordered arrivals, exactly 5 ms apart: intended, enqueued, attempted and completed all 120,000; drops, unscheduled requests, errors and incomplete requests all zero. All five nearest-rank percentile series match the log. **7,726 requests exceeded 250 ms.**

| Latency | p50 ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: |
| Scheduled to validated | 6.264600 | 319.624000 | **588.031143** |
| HTTP service | 5.854931 | 152.715202 | 277.767335 |
| Generator plus queue | 0.254344 | 157.578606 | 426.305938 |
| Generator | 0.221794 | 0.906199 | 1.065102 |
| Worker queue | 0.003535 | 157.205185 | 425.904449 |

All four closed-loop stages passed. The sustained test's 764.53 seconds comprise approximately 161.117 seconds setup, **600.000613 seconds measured load**, and 3.413 seconds after load ([phases](../evidence/final-master/independent-performance/phase-and-source-comparison.json)).

## Resources and bounded diagnosis

Go 1.26.11 / PostgreSQL 17.9 ran on four logical CPUs, AMD EPYC 9V74, approximately 16 GB RAM. Durability remained enabled. All 6,000 diagnostics samples retain request-pool maximum **4**; seven persistent connections include listener, backup gate and observer. Up to **64 additional storage coordination sessions/controller** are separate, unmeasured headroom.

[Resources](../evidence/final-master/independent-performance/final-resources.json): host idle 58.435%, I/O wait 5.235%, no swap; pool wait 2,653.122 seconds across 108,914 acquisitions. The combined Go process allocated 615,814.5 bytes/9,045.1 objects per request, used 404.278 CPU seconds including 46.074 GC seconds, and retained heap 96.765→100.895 MB. Application RSS was **not recorded**. Docker PostgreSQL memory peaked near 364.8 MiB across setup/load/drain.

[Historical comparison](../evidence/final-master/independent-performance/window-comparison.json) associates delayed windows with pool/worker queues and WAL waits: pool-full samples 93→1,604/6,000; mean WAL-sync time 0.179→0.804 ms; I/O wait 1.087→5.235%. The historical pass used Intel hardware. Protocol, load harnesses and query runtime are unchanged. These observations establish neither a sole hardware cause nor a reproduced code defect. No corrective patch or retry is justified; historical p99 22.221162 ms cannot qualify this candidate.

## Data and retained failures

[Exact-source control-flow review](../evidence/final-master/independent-performance/executed-data-assertions.json) shows physical SQL counts and complete owner traversals completed without reported failure before the sole latency assertion. No detached database dump exists; the conservative generic integrity verdict remains UNPROVEN because the whole test failed.

The [recovered historical ZIP](../evidence/final-master/historical-failed-gate/original-artifact.zip) independently retains **547 drops/errors**, 119,453 completions and its original FAIL; [retention verification](../evidence/final-master/independent-performance/historical-failure-retention.json) passes 9/9 checks. Its hash matches the prior record. Git-retained archives preserve evidence beyond Actions expiry; historical missing durability fields remain unknown. [Manifest and reproduction](../evidence/final-master/independent-performance/README.md) identify raw inputs and limitations.
