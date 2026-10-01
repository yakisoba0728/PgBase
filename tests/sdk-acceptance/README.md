# SDK real-HTTP acceptance

`admin_http.mjs` and `sdk/dart/tool/admin_http_acceptance.dart` are external
TypeScript and Dart consumers used by
`pgbase/sdk_administration_acceptance_integration_test.go`. The Go test creates
an isolated PostgreSQL database, starts the real PgBase binary, builds the
TypeScript SDK, and launches Node with credentials and a task-owned backup
archive passed only through environment variables.

The acceptances cover administrator login and account management, schema
export/import preview/apply/status, asynchronous backup creation/status, and
gzip upload/download/list/delete with byte-for-byte verification. It uses the
real HTTP middleware and handlers; it is intentionally separate from SDK fetch
fixtures.

Run them with PostgreSQL 17 client tools, Node, Dart, and the integration
database URL configured:

```powershell
go test -tags='integration multiprocess' ./pgbase -run '^TestAdministrationSDKsAgainstRealHTTPServer$' -count=1 -v
```

Each external process has a 90-second execution bound. Backup creation must
reach `completed` and publish an archive; the uploaded synthetic archive is
only used to verify binary upload/download/list/delete and is not claimed to be
a restorable database dump. The TypeScript subtest installs its locked
development dependency with lifecycle scripts disabled before building.
The Dart subtest performs a bounded `dart pub get` before `dart run`, so the
consumer does not depend on a package configuration left by an earlier job.
