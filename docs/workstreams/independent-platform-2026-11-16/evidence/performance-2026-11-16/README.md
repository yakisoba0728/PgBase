# Performance qualification evidence

These are portable text/JSON extracts from the local PostgreSQL 17.9 qualification. `measurements.json` contains all paired native and mixed HTTP samples as reported by their harnesses. The two plan JSON files contain actual native first-page statements executed with `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` in separate diagnostic runs. Profile excerpts include setup, warmup and every workload in each Go test process; they are not per-request CPU or peak-RSS measurements.

`manifest.json` records source revisions and SHA-256 hashes. Raw profiles and compiled binaries are deliberately omitted; the commands in [performance.md](../../performance.md) recreate them. Only the original `8d0ceddb` mixed harness generated the paired measurement table. The stronger `15b19c36` assertions have separate functional smoke evidence. Current full-candidate CI results belong in the workstream validation document.

The first historical database attempts failed because the managed execution environment did not retain a PostgreSQL daemon across commands and then lacked the `pg_trgm` extension. Those failures were not product test failures and were not included in measurement data. Final runs use a private, disposable, fully initialized cluster with the server and test process sharing one command lifetime.
