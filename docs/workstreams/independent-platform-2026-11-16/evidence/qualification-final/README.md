# Final runtime qualification evidence

Source runtime: `5e4b71d1f602c9c136809bf65f2375bf75fc4a9f`.
Tested PR merge: `2d712ba6239f2d19f1329400848173842ed7874c`.
Both trees: `4599bc705d4c4ec440e273cc6db3397f3edcc11d`.
Run: [43780150632](https://github.com/yakisoba0728/PgBase/actions/runs/43780150632).

The [independent final review](security-final-artifact-review.md) and [machine-readable classifications](security-final-artifact-review.json) reconcile every final source/image finding and independently verify actual data bytes. All six ZIPs are unchanged downloaded artifact bytes. Their hashes were compared with the Actions artifact API; [manifest.json](manifest.json) retains IDs, dates, sizes and hashes. The original source-analysis summaries still say `classification: unreviewed`, as produced by the automated parser. The separate reviewed reconciliation must not be confused with edited scanner output.

| File | Content |
| --- | --- |
| go.zip | Full-scope Go SARIF, all 24 normalized findings and parser summary |
| js.zip | Full-scope JavaScript/TypeScript SARIF, one normalized finding and parser summary |
| image.zip | HIGH/CRITICAL-only SARIF (zero results) and all-severity SARIF (32 results) |
| runtime-assets.zip | Actual final image certificate files/fingerprints, public trust environment, timezone source, package status and manifest |
| sbom.zip | SPDX inventory for the built image |
| mixed.zip | Final candidate's 4000-request strengthened HTTP fixture and runner environment; not a paired performance comparison |

The source-analysis jobs have no incremental mode and no PR diff-range extension. Logs explicitly skip the diff-informed stage because no precomputed ranges exist; no PR diff-range query pack appears in the CLI invocation. Go loads 37 queries and reports 483 extracted files/35 descriptors, 24 results (14 error/10 warning). JavaScript loads 105 queries and reports 327 extracted files/103 descriptors, one warning. Exact relevant log excerpts are retained beside the ZIPs. Counts describe their respective measures, not interchangeable numbers of security checks.

All 25 remaining source results match original findings by **rule, source URI and partial fingerprints**. URI is necessary: the three example startup-log sites share partial fingerprints and must not be collapsed. Original entries no longer reported are Go 1 (template HTML), Go 10 (cursor allocation), Go 17 (MFA narrowing), JS 2–3 (example destination). Disappearance alone is not proof of a vulnerability fix; concrete source boundaries and regressions establish the decisions.

Final image ID: `sha256:2966e2bfca8988c3d3c87808f19ae2869279a32de07d08bd4a086f0a7106e8ba`.
Its actual CA bundle SHA-256 is `e9c7b63ee2623386e9b0a3ee32f499d01a8ea23dc83b8383ddff9f8e749543b7`, matching local generation. It contains 148 roots; both removed root fingerprints are absent. `SSL_CERT_FILE` is `/etc/ssl/certs/ca-certificates.crt`; actual tzdata source says `# version 2026f`, with matching package versions `20250419~deb12u2` and `2026f-0+deb12u1`.

The final image's 32 all-severity records are **21 MEDIUM, 9 LOW and 2 UNKNOWN**, comprising 26 libc6 and six libssl3 entries. The five removed records belong to CA data, timezone data and the unused x/crypto OpenPGP inventory. No suppression was introduced. See the separate individual applicability decisions and source conflicts in [the security review](../../security-review.md).

The [independent runtime review](qualification-final-runtime-review.md) verifies matrix SDK/S3/skip results, browser/recovery assertions and masked credentials. For all runtime/SDK/browser/platform job results and scope limits, use [validation.md](../../validation.md). Source/image success does not itself establish the status of the other jobs.
