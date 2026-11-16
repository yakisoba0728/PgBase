# Independent doctor schema-69 regression review

Exact source `bedac6a9f3da74886e5155bc2eb005e915a0bcd6`, tree `9b4a2e78540c3737f03f6fcc27398bfaa0424180`, passed one top-level test and all 12 subtests under PostgreSQL 17.9 / UTF8 / Go race instrumentation. Zero failures or skips. Package elapsed time: 3.872 s.

Only `internal/doctor/checks_integration_test.go` differs from published source `30523ddaf27f8ffebd8b59c5ef802186107a408e`; runtime code is unchanged. The corrected latest-schema guard expects 69, while separate prefixes now reject actual versions 67 and 68. Latest acceptance and dirty-checksum cases remain.

The narrow search for hardcoded latest-schema 68 expectations found no additional obsolete expectation. The three historical migration/restore boundaries remain unchanged. This review covers the specified regression and related expectations.

`provenance.json` records exact source/tree, source-file hash, command, passed cases, fixture/initial invocation distinction and log digests. Compressed logs preserve the actual independent execution and fixture shutdown.
