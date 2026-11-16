# Final-master evidence index

Status: **IN_PROGRESS — current integrated qualification pending**. This is the
canonical navigation map for the new final-master round. Historical PASS/FAIL
results retain their own source identities and do not qualify the new candidate.
The coordinator's [README](README.md) and final qualification report own the
current acceptance decision.

## Start here

| Question | Canonical entry point | Primary evidence |
| --- | --- | --- |
| What is changing relative to main? | [Change map](change-map.md) | [Initial inventory](../evidence/final-master/initial-inventory.json), [live GitHub provenance](../evidence/final-master/github-provenance.json) |
| What can block acceptance or deployment? | [Risk register](risk-register.md) | Source-bound reports linked in each row |
| How is schema 69 installed or recovered? | [Migration and rollback](migration-and-rollback.md) | Existing executable upgrade/restore contracts and their source-bound CI logs |
| Which old evidence remains intact? | [Machine-readable manifest](evidence/manifest.json) | Every preserved file's Git blob, byte count and independently recomputed SHA-256 |
| What did the performance reviewer change? | [Performance/infrastructure review](performance-infrastructure-review.md) | [Author manifest](../evidence/final-master/performance/manifest.json), RED/GREEN logs and historical-log replay |
| What did the previous complete qualification prove? | [Historical integrated report](../final-integrated-qualification.md) | [Historical functional logs](../evidence/final-integrated/functional/raw-log-manifest.json), [performance archive](../evidence/final-integrated/performance/final-gate/pgbase-final-mixed-http.zip), [security archives](../evidence/final-integrated/independent/security/) |

New backend, SDK/UI, independent-QA and final-CI reports are linked from the
coordinator's current README as they are integrated. Their author-side tests and
independent reviews remain distinct. No new full CI or scanner result is implied
by this index's creation.

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
These are historical measurements; the new final candidate requires its own
qualification. Full source/job/step mappings remain in the
[functional result](../evidence/final-integrated/functional/functional-result.json)
and [artifact index](../evidence/final-integrated/coordinator/artifact-index.json).

## Classification and preservation

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

The historical performance ZIP, five security/runtime/SBOM ZIPs and 14 complete
functional logs are durable Git copies. The old 51 MB release-phase archive and
three small benchmark archives remain API references with recorded digests and
expiry; this round neither deletes a local original nor pretends those remote
objects were independently downloaded. Existing source history is retained.
The [artifact index](../evidence/final-integrated/coordinator/artifact-index.json)
distinguishes API-reported hashes from local recomputation.

To rebuild the current additive catalog and verify its bytes from the repository
root:

```sh
python3 docs/workstreams/independent-platform-2026-11-16/final-master/evidence/build_manifest.py
python3 docs/workstreams/independent-platform-2026-11-16/final-master/evidence/build_manifest.py --verify
```

Rebuilding is documentation processing, not a test rerun. The verifier requires
all 617 original evidence files to remain present and identical, checks indexed hashes,
and checks the 18 duplicate groups. It leaves original manifests untouched.
