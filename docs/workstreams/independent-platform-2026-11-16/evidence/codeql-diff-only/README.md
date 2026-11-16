# Rejected whole-source qualification evidence

Run [43777701630](https://github.com/yakisoba0728/PgBase/actions/runs/43777701630), source runtime `7619dbf790c9ccf88079247482b5db18fe8f018a`, PR merge `e719d9d31655d4e6e4724bbb1dda5007235bc7e5`. These original ZIP bytes are retained unchanged.

| Language | Artifact | ZIP SHA-256 | Results | Extracted files |
| --- | --- | --- | ---: | ---: |
| Go | 14187510630 | 6510d15e13f0840b07f11ea0b2f519ad1f9b809e751a77ff9f797127dd1fa396 | 0 | 483 |
| JavaScript/TypeScript | 14187326152 | db70d97059ee3789145d761e0a3ada5405971c95970e0730f0ac5728d301a16d | 1 | 327 |

The analysis jobs succeeded, but SARIF has `incrementalMode: diff-informed` and the `codeql-action/pr-diff-range` extension. Logs identify 391 diff ranges in 148 changed files and use the corresponding extension pack. Full extraction does not imply unrestricted alert reporting. These are NOT whole-source zero/one-finding results. The revised parser rejects both reports when `--require-full-scope` is used, while preserving all evidence. See the separate final full-scope report.
