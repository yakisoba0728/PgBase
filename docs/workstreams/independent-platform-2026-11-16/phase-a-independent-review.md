# Phase A — independent correction reviews

Reviewer: `/root/backend_integration`, separate from the Performance and
Security implementers. This records the specific corrections reviewed in
addition to [the checkpoint Backend review](phase-a-backend.md). It is not a
whole-repository audit or evidence that the final CI/load gate passed.

## Load observer supervisor

Initial Performance commit `3dc1da70387f91a11862e100fd7fc2c1fa4d627d` removed
the unbounded shell EXIT-trap wait and preserved workload exit status. Its four
subprocess tests independently passed. Source review by the Coordinator raised
a remaining process-group boundary: the supervisor stopped waiting when the
group leader exited, even if a descendant ignored SIGTERM.

The independent reviewer reproduced both concrete cases with actual child
processes: a leader that had already exited and a leader that exited on SIGTERM.
In both cases the descendant remained running in state `S`. The reviewer then
stopped only its own fixture groups and reaped their children. These failures
are preserved in `evidence/phase-a-backend/performance-descendant-before.json`
and the exact standalone scripted reproduction, rather than overwritten by the
successful follow-up.

Performance follow-up `ee98f5eb13b97ec1a225cf780f17006f61b1e8c1` checks the
process group even after leader exit, allows a bounded TERM grace, force-stops
the group if still present, and bounds/reaps its direct child's wait. Independent
execution of all **six** subprocess regressions passed in **0.799 seconds**.
The separate exact two-case fixture now also passed: both descendants stopped
after approximately **0.102 seconds** of cleanup. Their briefly retained zombie
state is distinguished from a live child; the fixture's subreaper reaped both.

The supervisor keeps a nonzero workload result, including termination by
signal. An early-exited or insufficient-output observer fails an otherwise
passing workload. Setup/cleanup errors are surfaced. The workflow still uses
`set -euo pipefail`, its original Go load invocation and original fixed-arrival
criteria. It changes no server source, rate/duration/pool/latency threshold,
zero-loss/error assertion, or raw-arrival completeness check.

**Decision:** accept the corrected supervisor through `ee98f5eb` for the
candidate CI. This acceptance concerns cleanup and result reporting; it does
not turn the historical failing HTTP workload into a success.

## Variant cleanup split

The Security reviewer demonstrated that the checkpoint's one-second variant
cleanup deadline can end before the publishing transaction later rolls back.
Unlike the normal upload path, that path had not durably enqueued the object
for deletion before returning, so it could lose the only cleanup intent.

Corrective commit `606743b16bbeae4cbaf040567f6fb7026c071cd9` restores only
`internal/filestore/variants.go` to the exact initial PR `10dc0f8b` bytes. It
retains normal-upload cleanup bounds, file/range and Auth fixes. Its new
late-publisher-rollback regression checks durable enqueue, drains the actual
cleanup queue and verifies that the physical object is gone. The existing
late-commit reference-safety regression remains separate and retained.

Independent source review found no blocker in that narrow split. The new
rollback regression independently passed on disposable PostgreSQL17.9 with
Go race: **one top-level pass, no failure/skip**, package **2.810 seconds**.
It does not certify a new bounded variant algorithm: the old unbounded wait is
intentionally retained until a durable-intent design is separately reviewed.

The reviewer requested synchronization with the existing PostgreSQL lock-wait
helper before starting the delayed rollback, so a late-scheduled cleanup
goroutine cannot falsely pass the regression. Test-only follow-up
`3375bc20d5ac6d047ad823c7ebe33297741bf289` adds that synchronization. The exact
final test was independently rerun on that commit and passed PostgreSQL17.9 +
race: **one top-level pass, no failure/skip**, package **3.011 seconds**.
The Security reviewer separately reproduced the checkpoint failure with this
same synchronized test. That before run remains its evidence, not a second
execution claimed here.

**Decision:** accept the narrow variant split through `3375bc20`; defer a
bounded variant cleanup design until durable intent is guaranteed.

## Evidence and scope

The standalone reproduction is
`evidence/phase-a-backend/check_observer_descendants.py`. It takes the reviewed
`run-observed-load.py` path and commit identity as arguments, emits JSON, and
returns failure if either descendant remains live. Raw before/after outputs,
independent regression logs, exact PostgreSQL commands/source identities, and
file digests are retained in the accompanying independent-review manifest.

The final candidate may combine these accepted corrections with other reviewed
changes. Its own full CI, PostgreSQL matrix, SDK/UI/browser, scanners and
sustained-load results remain mandatory and must use its actual source SHA.
