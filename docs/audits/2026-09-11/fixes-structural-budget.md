# 20-defect fixes: structural budget

This note records the final measured scope for the approved 20-defect
correctness fixes. The comparison base is `d05452c293764287f8c548dc892414548a74d11e`.
The complete machine-readable result is
`.local/defect-fixes-2026-09-11/metrics.json`; the base snapshot is beside it
as `base-metrics.json`.

The repository-wide measurements are:

| metric | base | final | gross added | gross deleted | net |
| --- | ---: | ---: | ---: | ---: | ---: |
| handwritten runtime LOC | 97,941 | 98,637 | 864 | 168 | +696 |
| UI runtime LOC | 7,101 | 7,101 | 0 | 0 | 0 |
| production interfaces | 94 | 95 | — | — | +1 |
| exported Go declarations | 1,553 | 1,563 | — | — | +10 |
| `New*` constructors | 63 | 64 | — | — | +1 |
| production files over 800 LOC | 0 | 0 | — | — | 0 |

The tracked diff currently contains 76 files and 2,614 gross additions versus
245 deletions (+2,369 net). By repostats category, runtime is +856/-168
(+688), tests are +1,296/-36 (+1,260), support is +21/-0 (+21), config is
+435/-41 (+394), and docs are +6/-0 (+6). There are no generated-category
changes. The runtime-category total includes migration/support classification;
the handwritten runtime ceiling uses the runtime source measurement directly.

The data-group budget moves from 27,498 to 27,716 handwritten runtime lines,
with +840/-622 in the six budgeted packages and +218 net. The additions cover
relation correlation and rule compilation, mutation transaction outcomes and
hook publication, and migration snapshot metadata. Auth identity policy
validation is an additional correctness change outside the six-package data
group. The corresponding deletions remove obsolete paths inside the same
package seams, leaving the budget at the exact measured final value.

The scope is the 20 original findings: SFM-001 through SFM-003, RQR-001
through RQR-003, AUTHMAIL-001 through AUTHMAIL-002, EXT-AUD-001 through
EXT-AUD-008, OPS-F-001 through OPS-F-002, and SDK-AUDIT-001 plus
SDK-AUDIT-003. The audit ledger remains the source for each finding's focused
test and any follow-up limitation; this budget records only the structural
cost of that approved correctness scope.

The new public seams have narrow roles. `MutationErrorHook` and
`TransactionOutcome` let external transactions publish mutation success or
failure only after commit or rollback. `PublicAPIError` preserves branded
public errors through JavaScript routing. Administrator record-mail methods
render supplied OTP and alert inputs, so an alert or OTP delivery does not
mint a second credential. These boundaries support the correctness fixes and
account for the one interface, ten exported declarations, and one constructor
increase.

The existing classifier, tracked-source definitions, comment-preservation
rules, and 800-line production-file rule remain unchanged. No source was
excluded to create room and no budget value includes spare slack. The exact
limits are enforced by `internal/repositorypolicy/loc_budget_test.go`.
