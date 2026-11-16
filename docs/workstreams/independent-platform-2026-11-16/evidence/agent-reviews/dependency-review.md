# x/crypto v0.59.0 isolated dependency review

Agent commit: `e1939c0721dc938ce7218816ac1df971db0b6f99`; integrated as `c27b883945aceb54deefede73d8463440054e1d5` and published as [`54c6011a4cf68de7443a299c7624bf076d2b1575`](https://github.com/yakisoba0728/PgBase/commit/54c6011a4cf68de7443a299c7624bf076d2b1575). Only `go.mod` and `go.sum` changed. `go mod tidy` caused no unrelated edits, and comparison of every selected module version shows exactly `golang.org/x/crypto v0.58.0 -> v0.59.0`. Go1.26.0 language directive and Go1.26.11 toolchain remain unchanged.

## Official provenance

- Official proxy metadata: https://proxy.golang.org/golang.org/x/crypto/@v/v0.59.0.info
- Official go.mod: https://proxy.golang.org/golang.org/x/crypto/@v/v0.59.0.mod
- Origin: `https://go.googlesource.com/crypto`, tag `refs/tags/v0.59.0`, commit `6c966a7517bfc4fa0f268afb1fe663013ed932a5`, timestamp2026-11-11T07:23:42Z.
- Go requirement:1.26.0; upstream x/net0.62.0, x/sys0.50.0, x/term0.48.0 and x/text0.44.0 match the project's existing selection.
- Module checksum: `h1:xS2jhqYbzNrpvkWP2YTDf4TJwBy3ykR03i8vFDxEc7c=`.
- Module go.mod checksum: `h1:veDyUwATGs3c+vN3VfTbzViMGXeTBk3WyhBOkn+F/4U=`.

Downloaded with `GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org`; fetched the sum.golang.org signed lookup independently and compared both hashes. `go mod verify` reports `all modules verified`. Raw proxy, checksum, module-list and dependency-list evidence is retained in sibling xcrypto-* files.

## Reachability and compatibility

The official advisory https://vuln.go.dev/ID/GO-2026-5932.json flags x/crypto's OpenPGP subtree before the removal revision, and separately flags the legacy golang.org/x/crypto/openpgp module with no fixed version. Actual production `go list -deps ./...` output contains only `golang.org/x/crypto/argon2`, `blake2b`, `acme` and `acme/autocert`. There is no OpenPGP dependency before or after this update. This commit is dependency hygiene; it is not a fix for a demonstrated reachable PgBase OpenPGP vulnerability.

The complete source subtrees used by PgBase are byte-for-byte identical between versions: argon2 has9 files, blake2b11, and acme21 including autocert. Per-file SHA-256 equivalence is retained in `xcrypto-used-source-equivalence.json`. Existing password hash format, Argon2 parameters, autocert APIs and PgBase public APIs are unchanged. No database migration is needed.

Version0.59 removes the obsolete OpenPGP directory. A downstream Go application independently importing x/crypto/openpgp may need to remove or replace its own import when Go module selection chooses0.59. This is a concrete upstream removed-package compatibility boundary, although PgBase does not use it.

No performance improvement or regression is claimed. The actually linked x/crypto source is identical; this update is not the source of the earlier query allocation changes or measurements. No additional benchmark was justified for an unchanged execution path.

## Executed validation

`go test -race -p 1 ./internal/adminauth ./internal/userauth ./internal/passwordfield ./internal/mailer ./internal/cursor -count=1 -timeout=10m` passed:

- adminauth4.068s
- userauth1.749s
- passwordfield2.861s
- mailer1.019s
- cursor1.020s

`go test -race -tags=integration ./pgbase -run '^(TestACMEHTTP01IssuesCachesAndServesCertificate|TestRunManagedACMEIssuesCachesAndStopsChallenge)$' -count=1 -v -timeout=5m` passed both tests in5.487s. They exercise local mock-CA HTTP-01 validation, unknown-host rejection, issuance, renewal, cache reuse, serving and challenge-listener shutdown. No public CA or external production endpoint was used.

`go test -race -tags=integration ./internal/serviceauth -count=1 -v -timeout=5m` passed all3 tests in2.548s: token lifecycle/scope locking, serialized revocation and nontruncating scope lists.

All DB tests used the agent's private disposable PostgreSQL17.9 cluster, with server/test/shutdown inside one execution command. Both unit and integration race logs are retained. Normal test commands used no test skips or weakened assertions. An initial whole-project dependency-list command could not obtain VCS stamping metadata in this environment; it was repeated with `-buildvcs=false` for dependency enumeration only. This flag was not needed by the successful tests.

`git diff --check` passed; the final worktree is clean. Root owns combined-candidate qualification, source-equivalence/migration notes and GitHub publication.
