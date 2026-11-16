# Actual agent work and review snapshots

Three separate agents worked in isolated Git worktrees: Security, Backend/Performance and Frontend/SDK. The root integrated their changes, independently checked evidence and managed PR publication. These files describe actual delegation and execution; CI jobs and an unanswered GitHub review request are not counted as reviewers.

| Report | Scope |
| --- | --- |
| [security-agent.md](security-agent.md) | Original Go finding review, archive/TOTP/mail changes and affected-package test evidence |
| [security-cross-review.md](security-cross-review.md) | Independently reproduced SDK adoption/continuation/persistence defects, repaired-code checks and performance fixture review |
| [frontend-sdk-agent.md](frontend-sdk-agent.md) | JavaScript classification, example/UI/SDK changes, failing-before tests and complete standalone suites |
| [dependency-review.md](dependency-review.md) | x/crypto official provenance, used-source equivalence, compatibility and affected unit/integration race checks |
| [performance-final-review.md](performance-final-review.md) | Metric/source/artifact verification, document corrections and independent runtime-data review |
| [Final runtime review](../qualification-final/qualification-final-runtime-review.md) | Independent final PG/SDK/S3 and browser/crash/masking log verification |
| [Final artifact review](../qualification-final/security-final-artifact-review.md) | Independent full-scope25-result classification,32-image-record reconciliation and actual final data-byte verification |
| [security-final-doc-review.md](security-final-doc-review.md) | Exact original classification metadata, rationale and attribution checks at the documented review snapshot |
| [first-runtime-backend-summary.md](first-runtime-backend-summary.md) | First combined CI package/skip/S3/SDK evidence and the discovered live-test selection gap |
| [sdk-all-live-pg17.log](sdk-all-live-pg17.log) | Additional actual local execution of all four live contracts before expanding CI |

The compact `xcrypto-*` evidence is included here; the portability issue identified by the performance reviewer was resolved after its snapshot. Reports may retain original local paths and source commit IDs. [The commit map](../runtime-commit-map.json) records published tree/patch equivalents, including agent aliases. Large original local test logs and decoded CI logs are not all copied into the repository; reports identify their hashes/commands and GitHub job links. The original and final SARIF artifact bytes and signed runtime-data provenance are preserved separately in this evidence directory.

A snapshot's file hashes describe that review moment. Later root edits recording observed final CI do not retroactively change what an agent reviewed. Current combined results and remaining limits are in [validation.md](../../validation.md).
