# Final-master evidence index

Status: **BLOCKED / NOT_READY — current mandatory p99 gate failed**. This is the
canonical navigation map for the new final-master round. Historical PASS/FAIL
results retain their own source identities and do not qualify the new candidate.
The coordinator's [README](README.md) and final qualification report own the
current acceptance decision.

## Start here

The **2026-11-17 bounded recovery** ended `BLOCKED / NOT_READY`: zero runtime patches and zero new final-gate runs. See [recovery report](performance-blocker-recovery.md), [three-hypothesis diagnosis](../evidence/final-master/performance-blocker-recovery/agent-a/diagnosis.md), [focused runtime controls](../evidence/final-master/performance-blocker-recovery/agent-b/runtime-review.md), and [separate QA](../evidence/final-master/performance-blocker-recovery/agent-c/independent-qa.md). The [previous manifest](../evidence/final-master/performance-blocker-recovery/evidence-manifest-before.json) is retained verbatim; the current machine manifest includes the new evidence and refreshed canonical-document hashes while preserving the original 617-file baseline.

| Question | Canonical entry point | Primary evidence |
| --- | --- | --- |
| What is changing relative to main? | [Change map](change-map.md) | [Initial inventory](../evidence/final-master/initial-inventory.json), [live GitHub provenance](../evidence/final-master/github-provenance.json) |
| What can block acceptance or deployment? | [Risk register](risk-register.md) | Source-bound reports linked in each row |
| How is schema 69 installed or recovered? | [Migration and rollback](migration-and-rollback.md) | Existing executable upgrade/restore contracts and their source-bound CI logs |
| Which old evidence remains intact? | [Machine-readable manifest](evidence/manifest.json) | Every preserved file's Git blob, byte count and independently recomputed SHA-256 |
| What did the performance reviewer change? | [Performance/infrastructure review](performance-infrastructure-review.md) | [Author manifest](../evidence/final-master/performance/manifest.json), RED/GREEN logs and historical-log replay |
| What did the previous complete qualification prove? | [Historical integrated report](../final-integrated-qualification.md) | [Historical functional logs](../evidence/final-integrated/functional/raw-log-manifest.json), [performance archive](../evidence/final-integrated/performance/final-gate/pgbase-final-mixed-http.zip), [security archives](../evidence/final-integrated/independent/security/) |

## Current qualification and raw evidence

| Review | Current source-bound result | Primary records |
| --- | --- | --- |
| [Final qualification](final-qualification.md) | Run43982303611, attempt1:14successful jobs and one failed mixed-http job;92/93required substantive steps pass. | [Full source/job/archive manifest](../evidence/final-master/final-ci/manifest.json), all15 decoded logs, final API snapshots |
| [Independent final CI](final-ci-independent.md) | All15 checkouts match candidate89e883fc / syntheticb5c17b6 / treec9a4c21; all65changed non-doc paths accounted for; overallBLOCKED. | [Independent source/step/hash checks](../evidence/final-master/independent-final-ci/) |
| [Independent performance](final-performance-independent.md) |120000complete,0drops/errors/incomplete; p99 588.031143ms>250. Raw quantiles independently reproduce the failure. | [Original final ZIP](../evidence/final-master/final-ci/artifacts/final-mixed-http.zip), [verification and bounded diagnosis](../evidence/final-master/independent-performance/) |
| [Fresh security](final-security-review.md) |25source warnings with scoped path dispositions;32image records,30needs_review and2amd64 exclusions. | [Exact57-record triage](../evidence/final-master/fresh-security/triage.json), five original security/SBOM/runtime archives |
| [Correction QA](independent-qa.md) | Separate original-source RED / corrected-source GREEN for four accepted P2 correction areas. | [Reviewer execution records](../evidence/final-master/independent/) |
| Historical failure recovery | Run43820007603 remains FAIL with547drops/errors; original archive recovered and9/9retention checks pass. | [Original failed ZIP and metadata](../evidence/final-master/historical-failed-gate/), [independent retention verification](../evidence/final-master/independent-performance/historical-failure-retention.json) |

All10current hosted archives were downloaded and matched to API sizes and SHA-256. **Nine original ZIPs are retained in Git**, including all three small PostgreSQL benchmark archives. The51,295,460-byte release binary ZIP is retained only on Actions; both original phase manifests and all eight executable hashes are durable repository evidence. Its verified hosted expiry is **2027-02-15 08:47:34 KST**. A future rebuild is not claimed byte-identical. [Artifact retention](../evidence/final-master/final-ci/artifact-retention.json) records every archive/member hash, job/source and expiry.

Current failure ZIP:3,717,310bytes, SHA-256 `560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393`. Recovered historical failure ZIP:3,451,697bytes, SHA-256 `d2411a4837ffd38a5fd3c2b67423240f07de4eed9ed0f5d4038af59ca628cf6e`. Both preserve their original failed output and complete available CSV; the older artifact lacks some durability metadata, which stays unknown.

Each job log is the connector-decoded UTF-8 response, deterministically compressed with a verified lossless roundtrip. Both decoded and stored hashes are retained. No identity claim is made for an original HTTP log-download ZIP. New verification scripts parse retained bytes; they do not launch workloads or scanners.

Later commits contain only documentation/evidence. [Publication mapping](../evidence/final-master/closeout-publication-map.json) distinguishes local and remote commit IDs; the final containing documentation HEAD is identified in PR148's latest handoff. The pre-consolidation PR description is retained [verbatim](../evidence/final-master/pr-description-before.md).

## Exact historical qualification identity

| Field | Preserved identity |
| --- | --- |
| Historical full run | [43861850923](https://github.com/yakisoba0728/PgBase/actions/runs/43861850923), attempt 1 |
| Tested PR source | `4091c3be20ec8db177886a2a85550924fce37ff5` |
| Actual synthetic checkout | `ba838872d72993abf1953dcafc64ba043f978c06` |
| Shared Git tree | `653a46758f9e6af9662b77ecfe2f069a2ac94cb4` |
| Previous documentation HEAD | `242f80aea730ee483cc22798fb6450bdb3064081`, tree `b758d273142d75377c18011d240cd64929cb4c97` |
| Difference after that test | One documentation/evidence commit, 84 files; no runtime/test/workflow change |
| Original performance ZIP SHA-256 | `82f0397e6b0ab5e1d71c96ebc2c4af35137fc8638d60c3c262b8208d5935d84e` |
| Hosted performance-artifact expiry | `2026-11-30T12:08:39Z`; the original ZIP is also retained in Git |

The historical run has 15 successful jobs. Its 200 RPS / 600 second result was
120,000 completed, zero drops/errors/incomplete requests and p99 22.221162 ms.
These remain historical measurements. The new final candidate
received its own qualification and failed the p99 gate; this historical pass does not supersede it. Full source/job/step mappings remain in the
[functional result](../evidence/final-integrated/functional/functional-result.json)
and [artifact index](../evidence/final-integrated/coordinator/artifact-index.json).

## Classification and preservation

The completed catalog contains **792 entries**:15CANONICAL,569HISTORICAL,205PRIMARY_EVIDENCE and3REDUNDANT. It is an additive navigation catalog, not792independent test executions. All617original files and18duplicate groups pass the final hash/byte verification;0original files are modified or removed. [Closeout verification](../evidence/final-master/closeout-verification.json) also confirms that all1,932non-document Git entries retain the frozen candidate's modes/blobs, and the folded continuation history is identical.

The manifest classifies each indexed item as `CANONICAL`, `PRIMARY_EVIDENCE`,
`HISTORICAL`, `REDUNDANT` or `EPHEMERAL`. Classification describes its role in
this round; `PRIMARY_EVIDENCE` can still be historical in time. The separate
`temporal_scope` and source fields prevent an old artifact from being read as a
current PASS. No existing entry is discarded or shortened.

The starting source `242f80a` contains **617** tracked files in repository paths
containing `/evidence/`: **615** in this workstream and **2** under the older
`docs/audits/2026-09-11-maintainability-implementation/evidence/` directory. These
are the original preservation set. The new `initial-inventory.json` and
`github-provenance.json` are separate additive reconnaissance records. The
manifest records the two original scopes explicitly and checks every preserved
byte against `242f80a`; later final-master evidence remains additive.

The 18 equal-Git-blob groups are also checked with SHA-256. Most are meaningful
separate observations: exit code `0`/`1`, empty stderr, PostgreSQL start/stop
messages, repeated command output, or author versus independent invocations.
Equal output does not establish an identical execution. Their separate paths and
provenance are preserved. Three duplicated historical API/metadata snapshots
have a preferred navigation target:

| Duplicate snapshot | Preferred copy | Retained contextual copy |
| --- | --- | --- |
| Final performance artifact metadata | `evidence/final-integrated/performance/final-gate/artifact-metadata.json` | `evidence/final-integrated/closeout-20261116/performance/artifact-metadata.json` |
| Full-run artifact API response | `evidence/final-integrated/functional/artifact-api.json` | `evidence/final-integrated/closeout-20261116/source-ci/artifacts.json` |
| Full-run jobs API response | `evidence/final-integrated/functional/jobs.json` | `evidence/final-integrated/closeout-20261116/source-ci/jobs.json` |

Those three secondary snapshots are marked `REDUNDANT` for navigation, with
`removal_allowed=false`: existing checksum ledgers and standalone closeout
records still address their original paths. Physical deletions, moves and
rewrites in the preservation set: **zero**. No `EPHEMERAL` classification is
asserted solely because a log is empty. This inventory is not a new exhaustive
secret scan.

## Missing metadata and expiry

SHA-256 and byte lengths are recomputed from the checked-out bytes. Source,
tree, run/job, command and expiry are inherited only from an explicitly scoped
sidecar or an exact path/hash match. A missing field is JSON `null`, accompanied
by a reason and the closest original sidecar when available. The manifest does
not treat the commit storing a log as the source that executed its test.

The prior run's historical performance ZIP, five security/runtime/SBOM ZIPs and14complete functional logs remain durable Git copies. That prior run's51MB release-phase archive and three benchmark archives retain their original API references/digests/expiry; no new download of those four old artifacts is claimed. This differs from the ten freshly downloaded current-run artifacts described above. Existing source history is retained.
The [artifact index](../evidence/final-integrated/coordinator/artifact-index.json)
distinguishes API-reported hashes from local recomputation.

To rebuild the current additive catalog and verify its bytes from the repository
root:

```sh
python3 docs/workstreams/independent-platform-2026-11-16/final-master/evidence/build_manifest.py --status BLOCKED
python3 docs/workstreams/independent-platform-2026-11-16/final-master/evidence/build_manifest.py --verify
```

Rebuilding is documentation processing, not a test rerun. The verifier requires
all 617 original evidence files to remain present and identical, checks indexed hashes,
and checks the 18 duplicate groups. It leaves original manifests untouched.
