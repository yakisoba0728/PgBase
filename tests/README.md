# Go test suites

Run the database-independent suite with:

```console
scripts/test-go-unit.sh -count=1
```

The command clears `PGBASE_TEST_ADMIN_URL`, excludes files guarded by the
`integration` build tag, and rejects every named skip that is not listed in
`go-test-skip-allowlist.tsv`.

Run the complete PostgreSQL suite with an administrator URL that can create
and drop isolated databases:

```console
export PGBASE_TEST_ADMIN_URL=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
scripts/test-go-integration.sh -count=1
```

The integration command checks `PGBASE_TEST_ADMIN_URL` before invoking
`go test`, builds with `-tags=integration`, runs unit and live tests together,
and applies the same skip policy. New live tests must use
`testutil.NewDatabase` and live in an `integration`-tagged file. When a test
file mixes pure and live cases, keep the pure cases in the ordinary file and
move only the live cases to a sibling `*_integration_test.go` file.

## R0 point-read compatibility decision

R0 preserves the current plain `Get` wire behavior: the fixed maximal
multi-relation fixture produces a 4,682,047-byte encoded record and `Get`
allows it. R0 must not introduce `response_too_large` for this point read.
Applying the 4 MiB response bound to plain `Get` requires a later, explicit
compatibility-tightening decision; the characterization work does not make
that contract change implicitly.
