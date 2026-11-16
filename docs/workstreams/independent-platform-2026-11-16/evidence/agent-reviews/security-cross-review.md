> Agent report snapshot. Local worktree paths and raw log filenames describe the agent’s scratch evidence at review time; those raw files are not all included here. Published source identities are recorded in [the commit map](../runtime-commit-map.json), and current combined CI evidence is in [validation](../../validation.md).

# Independent final SDK and performance review

Reviewer: Security subagent, read-only review of other agents' changes. No SDK or benchmark source edited by this reviewer.

## SDK scope and reproduced blockers

Reviewed TypeScript `e37d14e54543c7c844c33ac5e0f37b214b768d07` and Dart `fea6f05841dad89f21861c3dc6719df61e58fe5c`. Transport/body reads, current login/refresh/logout adoption, shared AuthStore changes, file hashing/preparation and upload header capture inspected.

1. **TypeScript simultaneous auth completion**: start two public logins under one generation, hold both fetch responses, then resolve valid A and B sessions synchronously. Both login promises fulfilled with distinct sessions, while only A was adopted. The low-level reader generation check ran before the outer authenticate continuation; that continuation skipped saving on mismatch but still returned B. Independently reproduced output: `outcomes=[fulfilled:a, fulfilled:b], adopted=a`.
2. **TypeScript final SDK continuation**: initial token old; start login B and a record get (also reproduced with file upload); wait until both fetches have started; resolve login B then old data synchronously. The login's authenticate continuation saves B before Records.get / Files.upload resumes its final await and returns the old result. These outer wrappers needed a final generation check.
3. **Dart pending persistence**: custom AuthStorage.write(token-a) blocks. Login A updates in-memory auth then awaits that write. Another auth.save(token-b) increments generation while persistence remains blocked. Releasing the old write let login A fulfill A while AuthStore contained B. Independently reproduced output: `status=fulfilled, returnedId=a, adoptedToken=token-b`. This is distinct from naive simultaneous Dart responses, which already rejected the second operation in our experiment.

Do not extend these claims to retroactively revoking already-settled promises. A direct delegated promise can be fulfilled before auth changes even if an application's `.then` runs later; consumers still need to discard returned data and clear session caches on auth change.

### Fix review and independent execution

Frontend agent added a TypeScript adoption helper checking before save and after exactly its own increment, guarding synchronous storage/listener reentrancy while retaining current-session storage errors; guarded wrapper mappings and final upload return. Dart per-operation AuthScope now tracks its own expected increment through async persistence and wraps full decode/adopt/return paths. All auth completion sites use these boundaries. Existing refresh/logout origin bookkeeping remains intact.

Independently executed against the final working changes before commit:

- `node --test test/auth-adoption.test.mjs`: 15 passed, 0 failed, 0 skipped; 168.6 ms. Includes public/realm registration/login, OTP, MFA, OAuth, shared-store refresh, reentrant persistence, record and upload continuation.
- `dart test test/auth_adoption_test.dart --reporter expanded`: 20 passed, 0 failed. Includes 18 delayed-persistence replacement cases and two controls preserving original current-session persistence errors.
- Original independent TS simultaneous-login and get/upload repros now reject stale outcomes with `session_changed`.
- Original independent Dart delayed-storage repro now rejects with `PgBaseError(403, session_changed)`.

No additional pre-upload identity leak found: generation is captured before async file reading/hashing, checked before constructing current auth headers, and response boundaries remain guarded. Already-sent mutation work is not cancelled or rolled back by these SDK checks. This was focused review, not complete SDK requalification or live server/browser testing.

Final immutable correction commits: `9010d059` (TypeScript) and `5bb0b580` (Dart). The committed runtime source matches the independently reviewed/tested working files; worktree is clean. README additions describe the final continuation/adoption boundary and expressly exclude revoking settled promises. No unresolved blocker in these committed corrections. Frontend agent independently reports full TS 109/109 and Dart 130/130 suites plus clean Dart analysis; those full suites were not rerun by this reviewer.

## Performance harness and instrumentation

Independently reviewed `8d0ceddb` (mixed HTTP fixture) and `8a390498` (native query plan/pool/GC instrumentation). No blocking sample-count, goroutine lifecycle, response-size, timing, ETag or owner-isolation issue found.

- Mixed requests are bounded by cycles*10. Each claimed cycle emits at most 10 samples; channel capacity matches that bound, so joining workers before draining does not deadlock.
- HTTP request timeouts, stage context and cleanup are bounded; response reads cap at 1 MiB+1 and reject over-limit payloads.
- Missing samples, context expiry, unexpected status and assertion failures fail the workload. Exact create/update IDs, ETag advancement, updated value, cross-owner get denial and before/after physical and per-owner row counts are checked.
- Pool and memory deltas surround the worker stage, not fixture setup. Per-request latencies include request encoding/HTTP/body read; total throughput includes client validation and harness work. In-process client allocations are correctly disclosed.
- Native EXPLAIN builds the actual native query plan with authorization/membership bindings and runs outside measurement. Its ANALYZE cache-warming effect is explicitly opt-in and documented. Native workload failures prevent reporting success metrics.

Found an assertion gap: original mixed page checks proved ownership, relation expansion, row count and exclusion of first-page IDs on continuation, but did not assert exact rank/filter/sort or within-page uniqueness. Performance agent fixed this in `15b19c36443c3031c5ed0d979ada326b05b9f19d`. Independently reviewed the follow-up: precomputed owner-specific titles are immutable; 50-row first pages require ranks 0..49 and continuation ranks 50..99; per-page ID map rejects duplicates; indices remain within 128-row owner fixtures. No blocker.

The performance agent reports a fresh 1280-request smoke for the stricter assertions. This reviewer did not rerun that load. Original paired timing/allocation measurements must remain attributed to `8d0ceddb`, because added per-page checks/allocations occur inside the full-stage measurement window. The follow-up does not retroactively change old benchmark evidence.
