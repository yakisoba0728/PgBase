# Dart SDK remaining surface

Development baseline: `abc94ef` on `codex/remaining-development`; this record describes subsequent changes.

The public client now wires TOTP disable to `POST /api/v1/auth/totp/disable`
with the server's exact `currentPassword` and `code` body. The administrator
client wires the five administrator-account routes and all seven managed-backup
routes, including exact gzip streaming for upload and byte-preserving download.
The existing schema-import operation lookup remains unchanged.

Compatibility review found that `confirmTotpEnrollment(fields:)` predates this
checkpoint (introduced by `758bc88f` and present at the `abc94ef` freeze). The
parameter has never been placed on the wire, while the native confirmation
route has a fixed recovery-code response and rejects projection semantics. It
therefore remains an ignored, deprecated named parameter for source
compatibility; new callers omit it. Removing it is reserved for a breaking SDK
revision.

Administrator binary transfers share the same in-memory cookie, CSRF,
generation, cancellation, 401 invalidation, and structured error handling as
administrator JSON requests. They remain separate from the public client's
bearer-authenticated request path. Restore uses the native no-body contract and
does not synthesize an approval or idempotency header.

Tests cover the package-level public surface, exact methods, paths, query and
JSON bodies, session/CSRF headers, streamed gzip bytes, content length, and
download bytes. Validation uses the pinned Dart 3.13.0 container image
`dart@sha256:8b6175f6c6b89aaf31ffdace4a22d17715c07f1cf3a772dadb10c658f779e23d`.

The download helper currently collects the archive into a `Uint8List`, matching
the SDK's existing download transport. Callers should persist or process it
promptly for large archives. The server route does not advertise a range
contract, so the SDK does not synthesize one.
