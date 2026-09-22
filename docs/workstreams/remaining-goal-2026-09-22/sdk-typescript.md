# TypeScript SDK administrative surface

Implemented the server-native TypeScript contracts for public TOTP removal, schema-import receipt lookup, all five administrator-account endpoints, and managed backup archive list/create/upload/download/delete/restore/job lookup.

The administrator client keeps its generic request transport private. Named mutation helpers preserve the browser cookie and in-memory CSRF boundary. Backup upload sends a caller-owned `Blob` as `application/gzip`; download buffers the response into a new `Blob`. Restore intentionally sends no idempotency header because the server route defines none. Schema import polling uses `/api/admin/schema/import/operations/{id}`, separate from collection schema operation receipts.

`BackupJob.phase` includes `interrupted`, the receipt returned when startup recovery marks a previously running persisted job as failed.

Freeze coverage is in `test/administrative-surfaces.test.mjs` and `test/public-api.test.ts`: URL encoding, exact request JSON, CSRF, gzip transfer bodies, cancellation, response types, and the absence of an invented restore idempotency header. The normal `npm test` command builds `dist`, checks strict source/dist public type conformance, and runs these transport fixtures.

Remaining limit: transport fixtures exercise real Fetch `RequestInit`/`Response` behavior but do not start the Go server or a PostgreSQL fixture. Repository-wide real-server acceptance remains outside this SDK workstream.
